#include "apn.h"
#include <stdio.h>
#include <stdlib.h>
static const char *str(const cJSON *j, const char *key) {
  const cJSON *v = cJSON_GetObjectItemCaseSensitive(j, key);
  return cJSON_IsString(v) ? v->valuestring : "";
}
int main(void) {
  char *line = NULL;
  size_t size = 0;
  if (getline(&line, &size, stdin) < 0)
    return 2;
  cJSON *q = cJSON_Parse(line);
  free(line);
  if (!q)
    return 2;
  cJSON *catalog = cJSON_GetObjectItemCaseSensitive(q, "catalog");
  cJSON *out =
      apn_resolve(catalog, cJSON_GetObjectItemCaseSensitive(q, "config"),
                  str(q, "home"), str(q, "spn"), str(q, "gid1"));
  cJSON_AddItemToObject(out, "profiles", apn_profile_list(catalog));
  char *raw = cJSON_PrintUnformatted(out);
  puts(raw);
  free(raw);
  cJSON_Delete(out);
  cJSON_Delete(q);
  return 0;
}
