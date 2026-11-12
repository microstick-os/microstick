#include "apn.h"
#include <glib.h>
#include <stdlib.h>
#include <string.h>

cJSON *apn_catalog(void) {
  const char *file = getenv("MICROSTICK_PROVIDERS");
  gchar *raw = NULL;
  gsize length = 0;
  if (!g_file_get_contents(file ? file : "/usr/share/microstick/providers.json",
                           &raw, &length, NULL))
    return NULL;
  cJSON *j = length <= 4 * 1024 * 1024 ? cJSON_Parse(raw) : NULL;
  g_free(raw);
  if (!cJSON_IsArray(j)) {
    cJSON_Delete(j);
    return NULL;
  }
  return j;
}

static const char *str(const cJSON *j, const char *key) {
  const cJSON *v = cJSON_GetObjectItemCaseSensitive(j, key);
  return cJSON_IsString(v) ? v->valuestring : "";
}
static gboolean preferred(const cJSON *j) {
  return cJSON_IsTrue(cJSON_GetObjectItemCaseSensitive(j, "preferred"));
}
static char *normalized(const char *s) {
  if (!s || !g_utf8_validate(s, -1, NULL))
    return g_strdup("");
  char *lower = g_utf8_casefold(s ? s : "", -1);
  GString *out = g_string_new("");
  for (const char *p = lower; *p; p = g_utf8_next_char(p)) {
    gunichar c = g_utf8_get_char(p);
    if (g_unichar_isalnum(c))
      g_string_append_unichar(out, c);
  }
  g_free(lower);
  return g_string_free(out, FALSE);
}
static gboolean selector(const cJSON *p) {
  return cJSON_GetArraySize(cJSON_GetObjectItemCaseSensitive(p, "spn")) > 0 ||
         *str(p, "gid1");
}
static gboolean sim_match(const cJSON *p, const char *spn, const char *gid) {
  char *name = normalized(spn);
  const cJSON *s;
  gboolean match = FALSE;
  cJSON_ArrayForEach(s, cJSON_GetObjectItemCaseSensitive(p, "spn")) {
    if (!cJSON_IsString(s))
      continue;
    char *expected = normalized(s->valuestring);
    match = match || (*name && !strcmp(name, expected));
    g_free(expected);
  }
  g_free(name);
  const char *expected = str(p, "gid1");
  if (*expected && gid && *gid &&
      !g_ascii_strncasecmp(gid, expected, strlen(expected)))
    match = TRUE;
  return match;
}
static gboolean same_settings(const cJSON *a, const cJSON *b) {
  const char *keys[] = {"apn", "username", "password", "auth", NULL};
  for (int i = 0; keys[i]; i++)
    if (strcmp(str(a, keys[i]), str(b, keys[i])))
      return FALSE;
  return TRUE;
}
static gboolean valid_apn(const char *s) {
  if (!*s || strlen(s) > 100)
    return FALSE;
  for (; *s; s++)
    if (!g_ascii_isalnum(*s) && *s != '.' && *s != '_' && *s != '-')
      return FALSE;
  return TRUE;
}
cJSON *apn_resolve(const cJSON *catalog, const cJSON *config, const char *home,
                   const char *spn, const char *gid1) {
  const char *mode = str(config, "profile"), *error = "apn_required",
             *state = "unknown";
  if (!*mode)
    mode = *str(config, "apn") ? "manual" : "auto";
  const cJSON *chosen = NULL, *p;
  int best = -1;
  gboolean ambiguous = FALSE, reviewed_home = FALSE;
  if (!strcmp(mode, "manual")) {
    if (valid_apn(str(config, "apn")))
      chosen = config;
    else
      error = "invalid_lte_parameters";
  } else if (!strcmp(mode, "auto")) {
    if (!home || !*home) {
      error = "apn_pending";
      state = "pending";
    } else {
      cJSON_ArrayForEach(p, catalog) {
        if (preferred(p) && !strcmp(str(p, "operator"), home))
          reviewed_home = TRUE;
      }
      cJSON_ArrayForEach(p, catalog) {
        if (strcmp(str(p, "operator"), home) || !valid_apn(str(p, "apn")))
          continue;
        if (reviewed_home && !preferred(p))
          continue;
        gboolean specific = selector(p);
        if (specific && !sim_match(p, spn, gid1))
          continue;
        int rank = (specific ? 100 : 0) + (preferred(p) ? 10 : 0);
        if (rank > best) {
          chosen = p;
          best = rank;
          ambiguous = FALSE;
        } else if (rank == best && chosen && !same_settings(chosen, p))
          ambiguous = TRUE;
      }
    }
  } else {
    cJSON_ArrayForEach(p, catalog) {
      if (strcmp(str(p, "id"), mode) || !valid_apn(str(p, "apn")))
        continue;
      if (chosen && !same_settings(chosen, p))
        ambiguous = TRUE;
      else
        chosen = p;
    }
    if (!chosen)
      error = "invalid_operator_profile";
  }
  if (ambiguous) {
    chosen = NULL;
    state = "ambiguous";
  }
  cJSON *out = cJSON_CreateObject();
  cJSON_AddBoolToObject(out, "ok", chosen != NULL);
  cJSON_AddStringToObject(out, "profile", mode);
  cJSON_AddStringToObject(out, "state", chosen ? "ready" : state);
  cJSON_AddStringToObject(out, "error", chosen ? "" : error);
  cJSON_AddStringToObject(out, "profile_id", chosen ? str(chosen, "id") : "");
  cJSON_AddStringToObject(out, "provider", chosen ? str(chosen, "name") : "");
  const char *keys[] = {"apn", "username", "password", "auth", NULL};
  for (int i = 0; keys[i]; i++)
    cJSON_AddStringToObject(out, keys[i], chosen ? str(chosen, keys[i]) : "");
  return out;
}
cJSON *apn_profile_list(const cJSON *catalog) {
  cJSON *out = cJSON_CreateArray();
  const cJSON *p;
  cJSON_ArrayForEach(p, catalog) {
    if (!preferred(p))
      continue;
    const cJSON *item;
    gboolean found = FALSE;
    cJSON_ArrayForEach(item, out) if (!strcmp(str(item, "id"), str(p, "id")))
        found = TRUE;
    if (found)
      continue;
    cJSON *entry = cJSON_CreateObject();
    cJSON_AddStringToObject(entry, "id", str(p, "id"));
    cJSON_AddStringToObject(entry, "name", str(p, "name"));
    cJSON_AddStringToObject(entry, "apn", str(p, "apn"));
    cJSON_AddItemToArray(out, entry);
  }
  return out;
}
