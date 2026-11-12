/* Read-only libext2fs allocation mapper. All I/O goes through a bounded parent
 * broker. There is no filesystem pathname, storage write or repair operation.
 * R offset length\n -> exactly length input bytes; M offset length\n; DONE\n.
 */
#define _FILE_OFFSET_BITS 64
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <errno.h>
#include <limits.h>
#include <ext2fs/ext2fs.h>
static uint64_t capacity;
static struct struct_io_manager broker;
static errcode_t open_io(const char *name, int flags, io_channel *out) {
    if (flags & IO_FLAG_RW) return EROFS;
    io_channel c = calloc(1, sizeof(*c));
    if (!c) return ENOMEM;
    c->magic = EXT2_ET_MAGIC_IO_CHANNEL; c->manager = &broker;
    c->name = strdup(name); c->block_size = 1024; c->refcount = 1;
    *out = c; return 0;
}
static errcode_t close_io(io_channel c) { free(c->name); free(c); return 0; }
static errcode_t blocksize(io_channel c, int n) { if (n < 512 || n > 65536) return EINVAL; c->block_size = n; return 0; }
static errcode_t read64(io_channel c, unsigned long long b, int count, void *dst) {
    if (count == INT_MIN || b > UINT64_MAX / (unsigned)c->block_size) return EINVAL;
    uint64_t off = b * c->block_size;
    uint64_t len = count < 0 ? (uint64_t)(-count) : (uint64_t)count * c->block_size;
    if (off > capacity || len > capacity - off || len > 16*1024*1024) return EINVAL;
    unsigned char *p = dst;
    while (len) {
        size_t n = len > 16384 ? 16384 : (size_t)len;
        if (printf("R %llu %zu\n", (unsigned long long)off, n) < 0 || fflush(stdout) || fread(p, 1, n, stdin) != n) return EIO;
        off += n; p += n; len -= n;
    }
    return 0;
}
static errcode_t read32(io_channel c, unsigned long b, int n, void *p) { return read64(c,b,n,p); }
static errcode_t write32(io_channel c, unsigned long b, int n, const void *p) { (void)c;(void)b;(void)n;(void)p;return EROFS; }
static errcode_t write64(io_channel c, unsigned long long b, int n, const void *p) { (void)c;(void)b;(void)n;(void)p;return EROFS; }
static errcode_t flush_io(io_channel c) { (void)c;return 0; }
static struct struct_io_manager broker = { .magic=EXT2_ET_MAGIC_IO_MANAGER,.name="microstick-read-broker",.open=open_io,.close=close_io,.set_blksize=blocksize,.read_blk=read32,.write_blk=write32,.read_blk64=read64,.write_blk64=write64,.flush=flush_io };
static int invalid_inode_block;
static int check_inode_block(ext2_filsys fs,blk64_t *block,e2_blkcnt_t count,blk64_t ref,int offset,void *data) {
    (void)count;(void)ref;(void)offset;(void)data;
    if(*block && (*block>=ext2fs_blocks_count(fs->super) || !ext2fs_test_block_bitmap2(fs->block_map,*block))) {invalid_inode_block=1;return BLOCK_ABORT;}
    return 0;
}
int main(int argc, char **argv) {
    char *end; ext2_filsys fs = NULL;
    const char *stage="geometry";
    if (argc != 2 && argc != 3) return 2;
    int candidate=argc==3 && !strcmp(argv[2],"--candidate");
    errno=0; capacity=strtoull(argv[1],&end,10);
    if(errno || *end || capacity<2048 || capacity%512) return 2;
    if(ext2fs_open("broker",EXT2_FLAG_64BITS,0,0,&broker,&fs)) return 3;
    uint64_t blocks=ext2fs_blocks_count(fs->super), bs=fs->blocksize;
    if(bs<1024 || bs>65536 || blocks>capacity/bs ||
       ext2fs_has_feature_encrypt(fs->super) || ext2fs_has_feature_bigalloc(fs->super)) goto bad;
    if(argc==3 && !candidate) {
        ext2_ino_t file_ino;struct ext2_inode file_inode;ext2_file_t file=NULL;
        if(argv[2][0]!='/' || strlen(argv[2])>512)goto bad;
        if(ext2fs_namei(fs,EXT2_ROOT_INO,EXT2_ROOT_INO,argv[2],&file_ino)){ext2fs_free(fs);return 4;}
        if(ext2fs_read_inode(fs,file_ino,&file_inode)||(file_inode.i_mode&LINUX_S_IFMT)!=LINUX_S_IFREG||EXT2_I_SIZE(&file_inode)>65536)goto bad;
        if(ext2fs_file_open(fs,file_ino,0,&file))goto bad;
        unsigned char data[65536];unsigned got=0;
        errcode_t error=ext2fs_file_read(file,data,(unsigned)EXT2_I_SIZE(&file_inode),&got);ext2fs_file_close(file);
        if(error||got!=EXT2_I_SIZE(&file_inode))goto bad;
        printf("F %u\n",got);if(fwrite(data,1,got,stdout)!=got)goto bad;
        puts("DONE");fflush(stdout);ext2fs_free(fs);return 0;
    }
    if((fs->super->s_state & EXT2_ERROR_FS)||(!candidate && (!(fs->super->s_state & EXT2_VALID_FS)||ext2fs_has_feature_journal_needs_recovery(fs->super))))goto bad;
    stage="group-descriptors";
    for(dgrp_t g=0; g<fs->group_desc_count; g++) {
        if(!ext2fs_group_desc_csum_verify(fs,g)) goto bad;
    }
    stage="bitmaps";
    if(ext2fs_check_desc(fs) || ext2fs_read_bitmaps(fs)) goto bad;

    /* A nominally clean superblock is not proof that file references agree
     * with the bitmap. Verify used inode data/extent/xattr blocks read-only. */
    if(!candidate) {
    stage="inode-references";
    ext2_inode_scan scan;
    if(ext2fs_open_inode_scan(fs,0,&scan))goto bad;
    ext2_ino_t ino;struct ext2_inode inode;
    for(;;){
        if(ext2fs_get_next_inode(scan,&ino,&inode)){ext2fs_close_inode_scan(scan);goto bad;}
        if(!ino)break;
        if(!ext2fs_test_inode_bitmap2(fs->inode_map,ino))continue;
        if(ino!=EXT2_BAD_INO && inode.i_mode==0)continue; /* reserved inodes */
        blk64_t acl=ext2fs_file_acl_block(fs,&inode);
        if(acl && (acl>=blocks || !ext2fs_test_block_bitmap2(fs->block_map,acl))){ext2fs_close_inode_scan(scan);goto bad;}
        if(ext2fs_inode_has_valid_blocks2(fs,&inode)){
            invalid_inode_block=0;
            if(ext2fs_block_iterate3(fs,ino,BLOCK_FLAG_READ_ONLY,NULL,check_inode_block,NULL) || invalid_inode_block){ext2fs_close_inode_scan(scan);goto bad;}
        }
    }
    ext2fs_close_inode_scan(scan);
    }
    uint64_t free_total=0;
    stage="free-counts";
    for(dgrp_t g=0; g<fs->group_desc_count; g++) {
        uint64_t free_group=0;
        for(blk64_t b=ext2fs_group_first_block2(fs,g); b<=ext2fs_group_last_block2(fs,g); b++)
            if(!ext2fs_test_block_bitmap2(fs->block_map,b)) free_group++;
        if(!candidate && free_group!=ext2fs_bg_free_blocks_count(fs,g)) goto bad;
        free_total+=free_group;
    }
    if(!candidate && free_total!=ext2fs_free_blocks_count(fs->super)) goto bad;
    uint64_t begin=0;
    for(uint64_t b=0; b<=blocks; b++) {
        int used = b<blocks && (b<fs->super->s_first_data_block || ext2fs_test_block_bitmap2(fs->block_map,b));
        if(used && begin==UINT64_MAX) begin=b;
        if(!used && begin!=UINT64_MAX) {
            printf("M %llu %llu\n",(unsigned long long)(begin*bs),(unsigned long long)((b-begin)*bs));
            begin=UINT64_MAX;
        }
    }
    /* Filesystem data partitions may exceed their ext4 capacity. This slack
     * contains no filesystem blocks; raw/calibration partitions never use this
     * mapper. Do not read hundreds of MiB of stock partition padding. */
    puts("DONE"); fflush(stdout); ext2fs_free(fs); return 0;
bad:
    fprintf(stderr,"ext4-map:%s\n",stage);
    ext2fs_free(fs); return 3;
}
