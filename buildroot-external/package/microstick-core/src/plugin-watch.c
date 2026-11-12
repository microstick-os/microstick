#define _GNU_SOURCE
#include <dirent.h>
#include <errno.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/inotify.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>
static volatile sig_atomic_t done;
static void stop(int s) {
  (void)s;
  done = 1;
}
static void rescan(const char *loader) {
  pid_t p = fork();
  if (!p) {
    execl(loader, loader, "rescan", NULL);
    _exit(127);
  }
  if (p > 0)
    while (waitpid(p, NULL, 0) < 0 && errno == EINTR) {
    }
}
static void watches(int fd, const char *base) {
  inotify_add_watch(fd, base,
                    IN_CREATE | IN_DELETE | IN_MOVED_FROM | IN_MOVED_TO |
                        IN_ATTRIB);
  DIR *d = opendir(base);
  if (!d)
    return;
  struct dirent *e;
  while ((e = readdir(d))) {
    if (e->d_name[0] == '.')
      continue;
    char p[1024];
    snprintf(p, sizeof(p), "%s/%s", base, e->d_name);
    struct stat st;
    if (!lstat(p, &st) && S_ISDIR(st.st_mode))
      inotify_add_watch(fd, p,
                        IN_CREATE | IN_DELETE | IN_MOVED_FROM | IN_MOVED_TO |
                            IN_ATTRIB | IN_DELETE_SELF | IN_MOVE_SELF);
  }
  closedir(d);
}
int main(int argc, char **argv) {
  if (argc != 4)
    return 2;
  signal(SIGTERM, stop);
  signal(SIGINT, stop);
  int fd = inotify_init1(IN_NONBLOCK | IN_CLOEXEC);
  if (fd < 0)
    return 1;
  watches(fd, argv[1]);
  pid_t child = fork();
  if (!child) {
    execlp("runsvdir", "runsvdir", argv[2], NULL);
    _exit(127);
  }
  if (child < 0)
    return 1;
  while (!done) {
    struct pollfd f = {fd, POLLIN, 0};
    int n = poll(&f, 1, 1000);
    if (n > 0) {
      char buf[16384];
      while (read(fd, buf, sizeof(buf)) > 0) {
      }
      /* Coalesce directory operations; state writes under .state are not
       * watched. */
      poll(NULL, 0, 100);
      while (read(fd, buf, sizeof(buf)) > 0) {
      }
      rescan(argv[3]);
      watches(fd, argv[1]);
    }
    if (waitpid(child, NULL, WNOHANG) == child) {
      done = 1;
      child = 0;
    }
  }
  if (child > 0) {
    kill(child, SIGTERM);
    while (waitpid(child, NULL, 0) < 0 && errno == EINTR) {
    }
  }
  close(fd);
  return 0;
}
