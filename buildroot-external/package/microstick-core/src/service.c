#define _GNU_SOURCE
#include <cjson/cJSON.h>
#include <errno.h>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/un.h>
#include <sys/wait.h>
#include <sys/signalfd.h>
#include <time.h>
#include <unistd.h>
#ifdef WEB
#include <arpa/inet.h>
#include <ifaddrs.h>
#include <microhttpd.h>
#include <openssl/crypto.h>
#include <openssl/evp.h>
#include <openssl/rand.h>
#endif
#define LIMIT 32768
#ifndef MICROSTICK_WEB_PORT
#define MICROSTICK_WEB_PORT 80
#endif
#ifndef MICROSTICK_WEB_UID
#define MICROSTICK_WEB_UID 405
#define MICROSTICK_WEB_GID 405
#endif
static const char *socket_path;
static volatile sig_atomic_t quitting;
static void stop(int sig) {
  (void)sig;
  quitting = 1;
}
static char *file(const char *p) {
  FILE *f = fopen(p, "rb");
  if (!f)
    return strdup("{}");
  char *b = calloc(1, LIMIT + 1);
  size_t n = fread(b, 1, LIMIT, f);
  fclose(f);
  b[n] = 0;
  return b;
}
static int save(const char *p, const char *s) {
  char t[1024];
  snprintf(t, sizeof(t), "%s.new", p);
  int f = open(t, O_WRONLY | O_CREAT | O_TRUNC | O_NOFOLLOW, 0600);
  if (f < 0)
    return -1;
  size_t n = strlen(s);
  int ok = write(f, s, n) == (ssize_t)n && fsync(f) == 0;
  close(f);
  if (!ok) {
    unlink(t);
    return -1;
  }
  return rename(t, p);
}
static const char *str(cJSON *j, const char *k) {
  cJSON *x = cJSON_GetObjectItemCaseSensitive(j, k);
  return cJSON_IsString(x) ? x->valuestring : "";
}
static int error_response(const char *response) {
  cJSON *j = cJSON_Parse(response);
  const char *e = str(j, "error");
  int error = !j || (*e && !cJSON_GetObjectItemCaseSensitive(j, "api_version"));
  cJSON_Delete(j);
  return error;
}
static char *rpc(const char *request) {
  int s = socket(AF_UNIX, SOCK_SEQPACKET | SOCK_CLOEXEC, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  snprintf(a.sun_path, sizeof(a.sun_path), "%s", socket_path);
  struct timeval tv = {2, 0};
  setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
  setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));
  char *b = calloc(1, LIMIT + 1);
  if (s < 0 || connect(s, (void *)&a, sizeof(a)) < 0 ||
      send(s, request, strlen(request), MSG_NOSIGNAL) < 0) {
    strcpy(b, "{\"error\":\"core_unavailable\"}");
  } else {
    ssize_t n = recv(s, b, LIMIT, 0);
    if (n <= 0)
      strcpy(b, "{\"error\":\"core_unavailable\"}");
  }
  if (s >= 0)
    close(s);
  return b;
}
#ifndef WEB
static const char *runpath(const char *name) {
  static char path[1024];
  const char *root = getenv("MICROSTICK_RUN");
  snprintf(path, sizeof(path), "%s/%s", root ? root : "/run/microstick", name);
  return path;
}
static char *state_rpc(const char *name, const char *request) {
  int fd = socket(AF_UNIX, SOCK_SEQPACKET | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
  struct sockaddr_un addr = {.sun_family = AF_UNIX};
  snprintf(addr.sun_path, sizeof(addr.sun_path), "%s", runpath(name));
  char *out = calloc(1, LIMIT + 1);
  ssize_t n = -1;
  if (fd >= 0 && !connect(fd, (void *)&addr, sizeof(addr)) &&
      send(fd, request, strlen(request), MSG_NOSIGNAL) >= 0) {
    struct pollfd p = {fd, POLLIN, 0};
    if (poll(&p, 1, 250) > 0)
      n = recv(fd, out, LIMIT, MSG_TRUNC);
  }
  if (fd >= 0) close(fd);
  if (n <= 0 || n >= LIMIT)
    strcpy(out, "{\"error\":\"core_unavailable\"}");
  return out;
}
static int core(void) {
  sigset_t children, previous;
  sigemptyset(&children);
  sigaddset(&children, SIGCHLD);
  if (sigprocmask(SIG_BLOCK, &children, &previous)) return 1;
  int child_events = signalfd(-1, &children, SFD_CLOEXEC | SFD_NONBLOCK);
  if (child_events < 0) return 1;
  int s = socket(AF_UNIX, SOCK_SEQPACKET | SOCK_CLOEXEC, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  snprintf(a.sun_path, sizeof(a.sun_path), "%s", socket_path);
  unlink(socket_path);
  if (s < 0 || bind(s, (void *)&a, sizeof(a)) || listen(s, 8))
    return 1;
  chown(socket_path, 0, 405);
  chmod(socket_path, 0660);
  pid_t worker = 0;
  unsigned seq = 0;
  char op[64] = "";
  const char *actions[] = {"scan", "connect", "forget", "ap",     "lte",
                           "pin",  "wifi",    "mode",   "ttl", "imei", "reboot", "lan", "lan_confirm", "lan_cancel", "firewall", "device", NULL};
  while (!quitting) {
    if (worker) {
      int status;
      if (waitpid(worker, &status, WNOHANG) > 0) {
        char *res = file(runpath("result.json"));
        if (!strstr(res, "\"ok\""))
          save(runpath("result.json"),
               "{\"ok\":false,\"error\":\"operation_failed\"}");
        free(res);
        unlink(runpath("request.json"));
        worker = 0;
      }
    }
    struct pollfd p[2] = {{s, POLLIN, 0}, {child_events, POLLIN, 0}};
    if (poll(p, 2, -1) <= 0)
      continue;
    if (p[1].revents & POLLIN) {
      struct signalfd_siginfo info;
      while (read(child_events, &info, sizeof(info)) > 0) {}
    }
    if (!(p[0].revents & POLLIN)) continue;
    int c = accept4(s, NULL, NULL, SOCK_CLOEXEC);
    if (c < 0)
      continue;
    struct ucred peer;
    socklen_t pn = sizeof(peer);
    if (getsockopt(c, SOL_SOCKET, SO_PEERCRED, &peer, &pn) ||
        !(peer.uid == geteuid() || peer.uid == 405)) {
      close(c);
      continue;
    }
    struct timeval tv = {1, 0};
    setsockopt(c, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
    char b[LIMIT + 1] = {0};
    ssize_t n = recv(c, b, LIMIT, MSG_TRUNC);
    char *out = NULL;
    if (n > 0 && n < LIMIT) {
      cJSON *j = cJSON_Parse(b);
      const char *m = str(j, "method");
      if (!strcmp(m, "status"))
        out = state_rpc("network.sock", b);
      else if (!strcmp(m,"wifi_confirm"))
        out = state_rpc("network.sock", b);
      else if (!strcmp(m, "config"))
        out = state_rpc("network.sock", b);
      else if (!strcmp(m, "lan_config"))
        out = state_rpc("router.sock", b);
      else if (!strcmp(m, "firewall_config"))
        out = state_rpc("router.sock", b);
      else if (!strcmp(m, "clients")) {
        cJSON *page = cJSON_GetObjectItemCaseSensitive(j, "page");
        if (page && (!cJSON_IsNumber(page) || page->valuedouble < 0 ||
                     page->valuedouble > 4096 || page->valuedouble != page->valueint))
          out = strdup("{\"error\":\"invalid_page\"}");
        else {
          out = state_rpc("router.sock", b);
        }
      } else if (!strcmp(m, "operation")) {
        cJSON *r = cJSON_CreateObject();
        cJSON_AddStringToObject(r, "id", op);
        cJSON_AddBoolToObject(r, "busy", worker != 0);
        char *result = file(runpath("result.json"));
        cJSON *v = cJSON_Parse(result);
        free(result);
        cJSON_AddItemToObject(r, "result", v ? v : cJSON_CreateObject());
        out = cJSON_PrintUnformatted(r);
        cJSON_Delete(r);
      } else {
        int allowed = 0;
        for (int i = 0; actions[i]; i++)
          if (!strcmp(m, actions[i]))
            allowed = 1;
        if (!allowed)
          out = strdup("{\"error\":\"unknown_method\"}");
        else if (worker)
          out = strdup("{\"error\":\"busy\"}");
        else if (save(runpath("request.json"), b))
          out = strdup("{\"error\":\"storage\"}");
        else {
          save(runpath("result.json"), "{}");
          snprintf(op, sizeof(op), "%ld-%u", (long)time(NULL), ++seq);
          worker = fork();
          if (worker == 0) {
            sigprocmask(SIG_SETMASK, &previous, NULL);
            close(child_events);
            close(c);
            close(s);
            execl(getenv("MICROSTICK_ACTION")
                      ? getenv("MICROSTICK_ACTION")
                      : "/usr/libexec/microstick-action",
                  "microstick-action", m, runpath("request.json"), NULL);
            _exit(127);
          }
          if (worker < 0) {
            worker = 0;
            out = strdup("{\"error\":\"spawn\"}");
          } else {
            cJSON *r = cJSON_CreateObject();
            cJSON_AddStringToObject(r, "operation", op);
            out = cJSON_PrintUnformatted(r);
            cJSON_Delete(r);
          }
        }
      }
      cJSON_Delete(j);
    }
    if (!out)
      out = strdup("{\"error\":\"invalid_request\"}");
    send(c, out, strlen(out), MSG_NOSIGNAL);
    free(out);
    close(c);
  }
  if (worker)
    kill(worker, SIGTERM);
  close(s);
  close(child_events);
  unlink(socket_path);
  return 0;
}
int main(int argc, char **argv) {
  socket_path = getenv("MICROSTICK_CORE_SOCKET");
  if (!socket_path)
    socket_path = "/run/microstick/control.sock";
  signal(SIGTERM, stop);
  signal(SIGINT, stop);
  if (argc > 1 && !strcmp(argv[1], "--daemon"))
    return core();
  char *r = rpc(argc > 1 ? argv[1] : "{\"method\":\"status\"}");
  puts(r);
  int fail = error_response(r);
  free(r);
  return fail;
}
#else
/* Static files have their own bound; RPC packets remain below 32 KiB. */
static char *asset(const char *p) {
  FILE *f = fopen(p, "rb");
  struct stat st;
  if (!f) return strdup("");
  if (fstat(fileno(f), &st) || st.st_size < 0 || st.st_size > 262144) {
    fclose(f); return strdup("");
  }
  size_t n = (size_t)st.st_size;
  char *b = calloc(1, n + 1);
  if (!b || fread(b, 1, n, f) != n) { free(b); b = strdup(""); }
  fclose(f);
  return b;
}
static char session[65], csrf[65], authpath[512], webroot[512];
struct login_session {
  char token[65], csrf[65];
  time_t expires, issued, until;
  char digest[65];
  int remembered;
};
static struct login_session sessions[8];
static char sessionspath[512];
static int cookie_age=1800;
static time_t next_login;
static time_t now_seconds(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return ts.tv_sec;
}
struct body {
  char data[LIMIT + 1];
  size_t used;
};
static void token(char *b) {
  unsigned char r[32];
  if (RAND_bytes(r, sizeof(r)) != 1)
    abort();
  for (int i = 0; i < 32; i++)
    sprintf(b + i * 2, "%02x", r[i]);
}
static void hex(const unsigned char *b, int n, char *out) {
  for (int i = 0; i < n; i++)
    sprintf(out + i * 2, "%02x", b[i]);
}
static int password(const char *pass, int set) {
  char *old = file(authpath);
  cJSON *j = cJSON_Parse(old);
  free(old);
  const char *salt = str(j, "salt");
  char newsalt[65];
  if (set) {
    token(newsalt);
    salt = newsalt;
  }
  unsigned char h[32];
  char hs[65];
  int ok = PKCS5_PBKDF2_HMAC(pass, strlen(pass), (void *)salt, strlen(salt),
                             120000, EVP_sha256(), 32, h);
  hex(h, 32, hs);
  if (set) {
    cJSON_Delete(j);
    j = cJSON_CreateObject();
    cJSON_AddStringToObject(j, "salt", salt);
    cJSON_AddStringToObject(j, "hash", hs);
    char *t = cJSON_PrintUnformatted(j);
    ok = save(authpath, t) == 0;
    free(t);
  } else
    ok = ok && strlen(str(j, "hash")) == 64 &&
         CRYPTO_memcmp(hs, str(j, "hash"), 64) == 0;
  cJSON_Delete(j);
  OPENSSL_cleanse(h, sizeof(h));
  return ok;
}
static void cookie_digest(const char *s,char out[65]) {
  unsigned char hash[32];unsigned len=0;
  EVP_Digest(s,strlen(s),hash,&len,EVP_sha256(),NULL);hex(hash,32,out);
}
static int sessions_save(void) {
  char *auth=file(authpath);cJSON *credentials=cJSON_Parse(auth);free(auth);
  cJSON *list=cJSON_CreateArray();
  for(unsigned i=0;i<8;i++)if(sessions[i].remembered&&sessions[i].digest[0]) {
    cJSON *v=cJSON_CreateObject();cJSON_AddItemToArray(list,v);
    cJSON_AddStringToObject(v,"hash",sessions[i].digest);
    cJSON_AddStringToObject(v,"auth_version",str(credentials,"hash"));
    cJSON_AddNumberToObject(v,"issued",sessions[i].issued);
    cJSON_AddNumberToObject(v,"expires",sessions[i].until);
  }
  cJSON_Delete(credentials);
  char *s=cJSON_PrintUnformatted(list);cJSON_Delete(list);int ok=s&&save(sessionspath,s)==0;free(s);return ok;
}
static void sessions_load(void) {
  char *auth=file(authpath);cJSON *credentials=cJSON_Parse(auth);free(auth);
  char *s=file(sessionspath);cJSON *list=cJSON_Parse(s),*v;free(s);unsigned i=0;time_t now=time(NULL);
  cJSON_ArrayForEach(v,list) {
    const char *h=str(v,"hash");cJSON *issued=cJSON_GetObjectItemCaseSensitive(v,"issued"),*until=cJSON_GetObjectItemCaseSensitive(v,"expires");
    if(i==8)break;
    if(strcmp(str(v,"auth_version"),str(credentials,"hash"))||strlen(h)!=64||strspn(h,"0123456789abcdef")!=64||!cJSON_IsNumber(issued)||!cJSON_IsNumber(until)||
       issued->valuedouble>now||until->valuedouble<=now||until->valuedouble-issued->valuedouble>2592000)continue;
    struct login_session *p=&sessions[i++];strcpy(p->digest,h);p->remembered=1;p->issued=issued->valuedouble;p->until=until->valuedouble;
    p->expires=now_seconds()+p->until-now;token(p->csrf);
  }
  cJSON_Delete(list);cJSON_Delete(credentials);
}
static int localhost(const char *h) {
  if (!h || strlen(h) > 80)
    return 0;
  char host[81];
  strcpy(host, h);
  char *p = strchr(host, ':');
  if (p) {
    char port[16];snprintf(port,sizeof(port),":%d",MICROSTICK_WEB_PORT);
    if (strcmp(p, port))
      return 0;
    *p = 0;
  }
  const char *run=getenv("MICROSTICK_RUN");char path[600];
  snprintf(path,sizeof(path),"%s/device.json",run?run:"/run/microstick");
  char *raw=file(path);cJSON *device=cJSON_Parse(raw);free(raw);
  char configured_name[80];snprintf(configured_name,sizeof(configured_name),"%s.local",str(device,"hostname"));
  cJSON *until=cJSON_GetObjectItemCaseSensitive(device,"previous_valid_until");
  int named=!strcmp(host,str(device,"mdns_name"))||!strcmp(host,configured_name)||
    (cJSON_IsNumber(until)&&now_seconds()<until->valuedouble&&!strcmp(host,str(device,"previous_mdns_name")));cJSON_Delete(device);
  if(named)return 1;
  struct ifaddrs *all = NULL;
  int ok = 0;
  if (getifaddrs(&all))
    return 0;
  for (struct ifaddrs *a = all; a; a = a->ifa_next) {
    char ip[INET_ADDRSTRLEN];
    if (a->ifa_addr && a->ifa_addr->sa_family == AF_INET &&
        inet_ntop(AF_INET, &((struct sockaddr_in *)a->ifa_addr)->sin_addr, ip,
                  sizeof(ip)) &&
        !strcmp(ip, host))
      ok = 1;
  }
  freeifaddrs(all);
  return ok;
}
static enum MHD_Result reply(struct MHD_Connection *c, unsigned code,
                             const char *b, const char *type, int cookie) {
  struct MHD_Response *r = MHD_create_response_from_buffer(
      strlen(b), (void *)b, MHD_RESPMEM_MUST_COPY);
  MHD_add_response_header(r, "Content-Type", type);
  MHD_add_response_header(r, "Cache-Control", "no-store");
  MHD_add_response_header(r, "X-Content-Type-Options", "nosniff");
  MHD_add_response_header(r, "X-Frame-Options", "DENY");
  MHD_add_response_header(r, "Content-Security-Policy",
                          "default-src 'self'; frame-ancestors 'none'; "
                          "base-uri 'none'; form-action 'self'");
  if (cookie) {
    char h[150];
    snprintf(h, sizeof(h),
             "microstick=%s; HttpOnly; SameSite=Strict; Path=/; Max-Age=%d",
             session, cookie > 0 ? cookie_age : 0);
    MHD_add_response_header(r, "Set-Cookie", h);
  }
  enum MHD_Result res = MHD_queue_response(c, code, r);
  MHD_destroy_response(r);
  return res;
}
static void complete(void *x, struct MHD_Connection *c, void **cls,
                     enum MHD_RequestTerminationCode why) {
  (void)x;
  (void)c;
  (void)why;
  if (*cls) {
    OPENSSL_cleanse(*cls, sizeof(struct body));
    free(*cls);
    *cls = NULL;
  }
}
static enum MHD_Result handle(void *x, struct MHD_Connection *c,
                              const char *url, const char *method,
                              const char *ver, const char *upload, size_t *size,
                              void **cls) {
  (void)x;
  (void)ver;
  const char *host = MHD_lookup_connection_value(c, MHD_HEADER_KIND, "Host");
  if (!localhost(host))
    return reply(c, 403, "{\"error\":\"host\"}", "application/json", 0);
  if (!*cls) {
    *cls = calloc(1, sizeof(struct body));
    if (!*cls)
      return MHD_NO;
    return MHD_YES;
  }
  struct body *b = *cls;
  if (*size) {
    if (b->used + *size > LIMIT)
      return reply(c, 413, "{}", "application/json", 0);
    memcpy(b->data + b->used, upload, *size);
    b->used += *size;
    *size = 0;
    return MHD_YES;
  }
  int post = !strcmp(method, "POST"), get = !strcmp(method, "GET");
  if (!post && !get)
    return reply(c, 405, "{}", "application/json", 0);
  if (post) {
    const char *origin =
        MHD_lookup_connection_value(c, MHD_HEADER_KIND, "Origin");
    char expected[100];
    snprintf(expected, sizeof(expected), "http://%s", host);
    if (!origin || strcmp(origin, expected))
      return reply(c, 403, "{\"error\":\"origin\"}", "application/json", 0);
    const char *ct =
        MHD_lookup_connection_value(c, MHD_HEADER_KIND, "Content-Type");
    if (!ct || strncmp(ct, "application/json", 16))
      return reply(c, 415, "{}", "application/json", 0);
  }
  if (get && (!strcmp(url, "/") || !strcmp(url, "/app.js") ||
              !strcmp(url, "/style.css") || !strcmp(url, "/lan.js") || !strcmp(url,"/lte.js") ||
              !strcmp(url, "/ui.js") || !strcmp(url, "/statistics.js") || !strcmp(url,"/favicon.svg"))) {
    char p[600];
    snprintf(p, sizeof(p), "%s/%s", webroot,
             !strcmp(url, "/") ? "index.html" : url + 1);
    char *data = asset(p);
    enum MHD_Result r =
        reply(c, 200, data,
              (!strcmp(url, "/app.js") || !strcmp(url, "/lan.js") || !strcmp(url,"/lte.js") ||
               !strcmp(url, "/ui.js") || !strcmp(url, "/statistics.js")) ? "application/javascript"
              : !strcmp(url, "/style.css") ? "text/css"
              : !strcmp(url,"/favicon.svg") ? "image/svg+xml"
                                           : "text/html; charset=utf-8",
              0);
    free(data);
    return r;
  }
  if (!strcmp(url, "/api/login") && post) {
    if (now_seconds() < next_login)
      return reply(c, 429, "{\"error\":\"wait\"}", "application/json", 0);
    cJSON *j = cJSON_Parse(b->data);
    int ok = password(str(j, "password"), 0);
    int remember=cJSON_IsTrue(cJSON_GetObjectItemCaseSensitive(j,"remember"));
    cJSON_Delete(j);
    if (!ok) {
      next_login = now_seconds() + 3;
      return reply(c, 401, "{\"error\":\"password\"}", "application/json", 0);
    }
    token(session);
    token(csrf);
    int slot = 0;
    for (int i = 1; i < 8; i++)
      if (sessions[i].expires < sessions[slot].expires)
        slot = i;
    int was_remembered=sessions[slot].remembered;
    memset(&sessions[slot],0,sizeof(sessions[slot]));
    strcpy(sessions[slot].token, session);
    strcpy(sessions[slot].csrf, csrf);
    cookie_age=remember?2592000:1800;
    sessions[slot].expires = now_seconds() + cookie_age;
    if(remember){sessions[slot].remembered=1;sessions[slot].issued=time(NULL);sessions[slot].until=time(NULL)+cookie_age;cookie_digest(session,sessions[slot].digest);}
    if((remember||was_remembered)&&!sessions_save()){memset(&sessions[slot],0,sizeof(sessions[slot]));return reply(c,503,"{\"error\":\"storage\"}","application/json",0);}
    char response[120];
    snprintf(response, sizeof(response), "{\"csrf\":\"%s\"}", csrf);
    return reply(c, 200, response, "application/json", 1);
  }
  const char *cookie =
      MHD_lookup_connection_value(c, MHD_COOKIE_KIND, "microstick");
  struct login_session *active = NULL;
  char digest[65]="";if(cookie&&strlen(cookie)==64)cookie_digest(cookie,digest);
  if (cookie && strlen(cookie) == 64)
    for (int i = 0; i < 8; i++)
      if (((sessions[i].remembered&&sessions[i].digest[0]&&!CRYPTO_memcmp(digest,sessions[i].digest,64)&&time(NULL)>=sessions[i].issued&&time(NULL)<sessions[i].until)||
          (!sessions[i].remembered&&sessions[i].token[0]&&!CRYPTO_memcmp(cookie,sessions[i].token,64)))&&
          now_seconds() <= sessions[i].expires)
        active = &sessions[i];
  if (!active)
    return reply(c, 401, "{\"error\":\"login\"}", "application/json", 0);
  if(!active->remembered)active->expires = now_seconds() + 1800;
  strcpy(csrf, active->csrf);
  if (post) {
    const char *t =
        MHD_lookup_connection_value(c, MHD_HEADER_KIND, "X-Microstick-CSRF");
    if (!t || strlen(t) != 64 || CRYPTO_memcmp(t, csrf, 64))
      return reply(c, 403, "{\"error\":\"csrf\"}", "application/json", 0);
  }
  if (get && !strcmp(url, "/api/session")) {
    char res[100];
    snprintf(res, sizeof(res), "{\"csrf\":\"%s\"}", csrf);
    return reply(c, 200, res, "application/json", 0);
  }
  if (post && !strcmp(url, "/api/logout")) {
    int durable=active->remembered;memset(active,0,sizeof(*active));
    if(durable&&!sessions_save())return reply(c,503,"{\"error\":\"storage\"}","application/json",0);
    session[0] = 0;
    return reply(c, 200, "{}", "application/json", -1);
  }
  if (post && !strcmp(url, "/api/password")) {
    cJSON *j = cJSON_Parse(b->data);
    const char *p = str(j, "password");
    int ok = strlen(p) >= 8 && strlen(p) <= 128 &&
             password(str(j, "current"), 0) && password(p, 1);
    cJSON_Delete(j);
    if(ok){memset(sessions,0,sizeof(sessions));sessions_save();session[0]=0;}
    return reply(c, ok ? 200 : 400, ok ? "{}" : "{\"error\":\"password\"}",
                 "application/json", ok?-1:0);
  }
  if(post&&!strcmp(url,"/api/wifi_confirm")) {
    const union MHD_ConnectionInfo *info=MHD_get_connection_info(c,MHD_CONNECTION_INFO_CONNECTION_FD);
    struct sockaddr_in local={0};socklen_t len=sizeof(local);char ip[INET_ADDRSTRLEN];
    if(!info||getsockname(info->connect_fd,(void *)&local,&len)||local.sin_family!=AF_INET||!inet_ntop(AF_INET,&local.sin_addr,ip,sizeof(ip)))return reply(c,409,"{}","application/json",0);
    cJSON *q=cJSON_Parse(b->data);if(!cJSON_IsObject(q)){cJSON_Delete(q);return reply(c,400,"{}","application/json",0);}
    cJSON_DeleteItemFromObjectCaseSensitive(q,"local_address");cJSON_AddStringToObject(q,"local_address",ip);
    cJSON_DeleteItemFromObjectCaseSensitive(q,"method");cJSON_AddStringToObject(q,"method","wifi_confirm");
    char *body=cJSON_PrintUnformatted(q);cJSON_Delete(q);char *res=rpc(body);free(body);
    enum MHD_Result r=reply(c,error_response(res)?409:200,res,"application/json",0);free(res);return r;
  }
  if (!strncmp(url, "/api/statistics", 15)) {
    const char *suffix = url + 15;
    const char *method_name = get && !*suffix ? "status" :
      get && !strcmp(suffix, "/series") ? "series" :
      get && !strcmp(suffix, "/history") ? "history" :
      post && !strcmp(suffix, "/reset") ? "reset_period" :
      post && !strcmp(suffix,"/config") ? "configure" : NULL;
    if (!method_name) return reply(c, 404, "{}", "application/json", 0);
    if(post){cJSON *input=cJSON_Parse(b->data);int object=cJSON_IsObject(input);cJSON_Delete(input);if(!object)return reply(c,400,"{}","application/json",0);}
    cJSON *request = cJSON_CreateObject();
    cJSON_AddStringToObject(request, "method", method_name);
    for (int i=0; i<2; i++) {
      const char *key = i ? "period" : "source";
      const char *value = MHD_lookup_connection_value(c, MHD_GET_ARGUMENT_KIND, key);
      if (value) cJSON_AddStringToObject(request, key, value);
    }
    if(post) {
      cJSON *input=cJSON_Parse(b->data);
      const char *fields[]={"source","wifi_persistence",NULL};
      for(unsigned i=0;fields[i];i++){cJSON *v=cJSON_GetObjectItemCaseSensitive(input,fields[i]);if(v){cJSON_DeleteItemFromObjectCaseSensitive(request,fields[i]);cJSON_AddItemToObject(request,fields[i],cJSON_Duplicate(v,1));}}
      cJSON_Delete(input);
    }
    char *body = cJSON_PrintUnformatted(request); cJSON_Delete(request);
    int fd = socket(AF_UNIX, SOCK_SEQPACKET|SOCK_CLOEXEC, 0);
    struct sockaddr_un addr = {.sun_family=AF_UNIX};
    const char *path = getenv("MICROSTICK_STATISTICS_SOCKET");
    snprintf(addr.sun_path, sizeof(addr.sun_path), "%s", path ? path : "/run/microstick/plugins/statistics/runtime/api.sock");
    struct timeval timeout = {0, 250000};
    setsockopt(fd,SOL_SOCKET,SO_RCVTIMEO,&timeout,sizeof(timeout));
    setsockopt(fd,SOL_SOCKET,SO_SNDTIMEO,&timeout,sizeof(timeout));
    char response[LIMIT+1]; ssize_t used = -1;
    if(fd>=0 && !connect(fd,(void *)&addr,sizeof(addr)) && send(fd,body,strlen(body),MSG_NOSIGNAL)>=0)
      used=recv(fd,response,LIMIT,MSG_TRUNC);
    free(body); if(fd>=0)close(fd);
    if(used<=0 || used>=LIMIT) return reply(c,503,"{\"error\":\"statistics_unavailable\"}","application/json",0);
    response[used]=0;
    cJSON *parsed=cJSON_Parse(response);
    int failed=!parsed|| (strcmp(method_name,"status")&&*str(parsed,"error"));
    cJSON_Delete(parsed);
    return reply(c,failed?409:200,response,"application/json",0);
  }
  const char *name = url + (!strncmp(url, "/api/", 5) ? 5 : 0);
  int valid = get && (!strcmp(name, "status") || !strcmp(name, "config") ||
                      !strcmp(name, "operation") || !strcmp(name, "clients") ||
                      !strcmp(name, "lan_config") || !strcmp(name, "firewall_config"));
  const char *actions[] = {"scan", "connect", "forget", "ap",     "lte",
                           "pin",  "wifi",    "mode",   "ttl", "imei", "reboot", "lan", "lan_confirm", "lan_cancel", "firewall", "device", NULL};
  for (int i = 0; post && actions[i]; i++)
    if (!strcmp(name, actions[i]))
      valid = 1;
  if (!valid)
    return reply(c, 404, "{}", "application/json", 0);
  cJSON *j = post ? cJSON_Parse(b->data) : cJSON_CreateObject();
  if (!cJSON_IsObject(j)) {
    cJSON_Delete(j);
    return reply(c, 400, "{}", "application/json", 0);
  }
  cJSON_DeleteItemFromObjectCaseSensitive(j, "method");
  cJSON_AddStringToObject(j, "method", name);
  if (get && !strcmp(name, "clients")) {
    const char *page = MHD_lookup_connection_value(c, MHD_GET_ARGUMENT_KIND, "page");
    if (page) {
      char *end; long value = strtol(page, &end, 10);
      if (!*page || *end || value < 0 || value > 4096) {
        cJSON_Delete(j);
        return reply(c, 400, "{\"error\":\"invalid_page\"}", "application/json", 0);
      }
      cJSON_AddNumberToObject(j, "page", value);
    }
  }
  char *q = cJSON_PrintUnformatted(j);
  cJSON_Delete(j);
  char *res = rpc(q);
  free(q);
  enum MHD_Result r =
      reply(c, error_response(res) ? 409 : 200, res, "application/json", 0);
  free(res);
  return r;
}
int main(void) {
  socket_path = getenv("MICROSTICK_CORE_SOCKET");
  if (!socket_path)
    socket_path = "/run/microstick/control.sock";
  const char *data = getenv("MICROSTICK_PLUGIN_DATA"),
             *dir = getenv("MICROSTICK_PLUGIN_DIR");
  if (!data || !dir)
    return 1;
  snprintf(authpath, sizeof(authpath), "%s/auth.json", data);
  snprintf(sessionspath,sizeof(sessionspath),"%s/sessions.json",data);
  sessions_load();
  snprintf(webroot, sizeof(webroot), "%s/www", dir);
  signal(SIGTERM, stop);
  signal(SIGINT, stop);
  struct MHD_Daemon *d = MHD_start_daemon(
      MHD_USE_INTERNAL_POLLING_THREAD, MICROSTICK_WEB_PORT, NULL, NULL, handle, NULL,
      MHD_OPTION_CONNECTION_LIMIT, (unsigned)16, MHD_OPTION_CONNECTION_TIMEOUT,
      (unsigned)10, MHD_OPTION_CONNECTION_MEMORY_LIMIT, (size_t)65536,
      MHD_OPTION_NOTIFY_COMPLETED, complete, NULL, MHD_OPTION_END);
  if (!d)
    return 1;
  if (setgid(MICROSTICK_WEB_GID) || setuid(MICROSTICK_WEB_UID)) {
    MHD_stop_daemon(d);
    return 1;
  }
  while (!quitting)
    sleep(1);
  MHD_stop_daemon(d);
  return 0;
}
#endif
