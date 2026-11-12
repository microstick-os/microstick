#define _GNU_SOURCE
#include "ssid.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef WIFI_TOOL
#include <cjson/cJSON.h>
#include <openssl/evp.h>
#endif
static int nibble(char c) {
  if (c >= '0' && c <= '9')
    return c - '0';
  if (c >= 'a' && c <= 'f')
    return c - 'a' + 10;
  if (c >= 'A' && c <= 'F')
    return c - 'A' + 10;
  return -1;
}
char *ssid_display(const char *s, gboolean iw, char hex[65]) {
  unsigned char raw[33];
  size_t n = 0;
  for (size_t i = 0; s[i] && n < 32; i++) {
    unsigned char c = s[i];
    if (c == '\\' && s[i + 1] == 'x' && s[i + 2] && s[i + 3] &&
        nibble(s[i + 2]) >= 0 && nibble(s[i + 3]) >= 0) {
      c = (nibble(s[i + 2]) << 4) | nibble(s[i + 3]);
      i += 3;
    } else if (!iw && c == '\\' && s[i + 1]) {
      char next = s[++i];
      c = next == 'n'   ? '\n'
          : next == 'r' ? '\r'
          : next == 't' ? '\t'
          : next == 'e' ? 27
                        : next;
    }
    raw[n++] = c;
  }
  for (size_t i = 0; i < n; i++)
    sprintf(hex + 2 * i, "%02x", raw[i]);
  hex[2 * n] = 0;
  raw[n] = 0;
  if (memchr(raw, 0, n) || !g_utf8_validate((char *)raw, n, NULL))
    return g_strdup_printf("SSID [%s]", hex);
  GString *out = g_string_new("");
  for (size_t i = 0; i < n; i++) {
    if (raw[i] < 32 || raw[i] == 127)
      g_string_append_printf(out, "\\x%02x", raw[i]);
    else
      g_string_append_c(out, raw[i]);
  }
  return g_string_free(out, FALSE);
}
#ifdef WIFI_TOOL
static const char *value(cJSON *j, const char *key) {
  cJSON *v = cJSON_GetObjectItemCaseSensitive(j, key);
  return cJSON_IsString(v) ? v->valuestring : "";
}
static int config(void) {
  char input[8193];
  size_t n = fread(input, 1, sizeof(input) - 1, stdin);
  input[n] = 0;
  cJSON *j = cJSON_Parse(input);
  if (!j)
    return 1;
  const char *s = value(j, "ssid"), *h = value(j, "ssid_hex"),
             *pass = value(j, "password");
  unsigned char raw[32], psk[32];
  size_t len = strlen(s);
  if (*h) {
    len = strlen(h) / 2;
    if (strlen(h) % 2 || len > 32 || !len)
      goto invalid;
    for (size_t i = 0; i < len; i++) {
      if (nibble(h[2 * i]) < 0 || nibble(h[2 * i + 1]) < 0)
        goto invalid;
      raw[i] = (nibble(h[2 * i]) << 4) | nibble(h[2 * i + 1]);
    }
  } else {
    if (!len || len > 32 || !g_utf8_validate(s, -1, NULL))
      goto invalid;
    memcpy(raw, s, len);
  }
  gboolean open = !strcmp(value(j, "security"), "open");
  if (!open && strcmp(value(j, "security"), "wpa2"))
    goto invalid;
  if (!open &&
      (strlen(pass) < 8 || strlen(pass) > 63 ||
       !PKCS5_PBKDF2_HMAC_SHA1(pass, strlen(pass), raw, len, 4096, 32, psk)))
    goto invalid;
  puts("ctrl_interface=/run/wpa_supplicant\nupdate_config=0\nnetwork={");
  printf(" ssid=");
  for (size_t i = 0; i < len; i++)
    printf("%02x", raw[i]);
  puts("");
  if (open)
    puts(" key_mgmt=NONE");
  else {
    printf(" psk=");
    for (unsigned i = 0; i < 32; i++)
      printf("%02x", psk[i]);
    puts("");
  }
  puts("}");
  cJSON_Delete(j);
  return 0;
invalid:
  cJSON_Delete(j);
  return 1;
}
static int scan(void) {
  cJSON *list = cJSON_CreateArray(), *row = NULL;
  char *line = NULL;
  size_t cap = 0;
  while (getline(&line, &cap, stdin) > 0) {
    line[strcspn(line, "\r\n")] = 0;
    if (g_str_has_prefix(line, "BSS ")) {
      if (cJSON_GetArraySize(list) >= 64)
        break;
      row = cJSON_CreateObject();
      cJSON_AddItemToArray(list, row);
      char b[18] = {0};
      sscanf(line + 4, "%17[0-9a-fA-F:]", b);
      cJSON_AddStringToObject(row, "bssid", b);
      cJSON_AddStringToObject(row, "security", "open");
      cJSON_AddStringToObject(row, "ssid", "");
      cJSON_AddNumberToObject(row, "signal", -100);
    } else if (row) {
      char *p = line;
      while (*p == ' ' || *p == '\t')
        p++;
      if (g_str_has_prefix(p, "SSID: ")) {
        char hex[65];
        char *name = ssid_display(p + 6, TRUE, hex);
        cJSON_ReplaceItemInObjectCaseSensitive(row, "ssid",
                                               cJSON_CreateString(name));
        cJSON_AddStringToObject(row, "ssid_hex", hex);
        g_free(name);
      } else if (g_str_has_prefix(p, "signal:"))
        cJSON_ReplaceItemInObjectCaseSensitive(
            row, "signal", cJSON_CreateNumber(g_ascii_strtod(p + 7, NULL)));
      else if (strstr(p, "Privacy"))
        cJSON_ReplaceItemInObjectCaseSensitive(
            row, "security", cJSON_CreateString("unsupported"));
      else if (g_str_has_prefix(p, "RSN:"))
        cJSON_ReplaceItemInObjectCaseSensitive(row, "security",
                                               cJSON_CreateString("wpa2"));
      else if (strstr(p, "Authentication suites:") &&
               (strstr(p, "802.1X") || (strstr(p, "SAE") && !strstr(p, "PSK"))))
        cJSON_ReplaceItemInObjectCaseSensitive(
            row, "security", cJSON_CreateString("unsupported"));
    }
  }
  free(line);
  char *s = cJSON_PrintUnformatted(list);
  puts(s);
  free(s);
  cJSON_Delete(list);
  return 0;
}
int main(int argc, char **argv) {
  if (argc != 2)
    return 2;
  return !strcmp(argv[1], "scan")     ? scan()
         : !strcmp(argv[1], "config") ? config()
                                      : 2;
}
#endif
