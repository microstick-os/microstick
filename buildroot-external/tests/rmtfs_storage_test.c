
#include <assert.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include RMTFS_STORAGE_SOURCE
int main(void) {
    _Static_assert(sizeof(size_t) == 8 && sizeof(off_t) == 8, "AArch64 widths");
    struct rmtfd fd = { .fd = -1, .shadow_len = 512 };
    fd.shadow_buf = malloc(fd.shadow_len);
    assert(fd.shadow_buf);
    memset(fd.shadow_buf, 0x5a, fd.shadow_len);
    storage_read_only = 1;
    const off_t offsets[] = {0, 256, 511, 512, 513, 1024, 4096};
    for (size_t k = 0; k < sizeof(offsets) / sizeof(offsets[0]); k++) {
        unsigned char out[512];
        memset(out, 0xcc, sizeof(out));
        assert(storage_pread(&fd, out, sizeof(out), offsets[k]) == sizeof(out));
        for (size_t i = 0; i < sizeof(out); i++)
            assert(out[i] == (offsets[k] + (off_t)i < 512 ? 0x5a : 0));
    }
    free(fd.shadow_buf);
    puts("PASS: 7 offsets; data preserved, partial/at/beyond EOF zero-filled");
}
