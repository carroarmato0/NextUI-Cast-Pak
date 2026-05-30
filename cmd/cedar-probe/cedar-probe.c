/* cedar-probe — validates Allwinner CedarC v1.3 H.264 hardware encode.
 * Encodes one synthetic 480x272 NV12 frame.
 * Output: /tmp/cedar-probe.h264 (raw Annex B H.264)
 * Exit 0 on success, 1 on any failure.
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <dlfcn.h>

/* ── CedarC v1.3.0 type definitions ────────────────────────────────────── */

typedef enum { VENC_CODEC_H264    = 0 } VENC_CODEC_TYPE;
typedef enum { VENC_PIXEL_YUV420SP = 0 } VENC_PIXEL_FMT;  /* 0 = first enum value */
typedef struct VideoEncoder VideoEncoder;

/* ScMemOpsS layout from sc_interface.h (needed to call open2 before first use) */
typedef struct ScMemOpsS {
    int   (*open)(void);
    int   (*open2)(void *, void *);
    void  (*close)(void);
    int   (*total_size)(void);
    void *(*palloc)(int, void *, void *);
    void *(*palloc_no_cache)(int, void *, void *);
    void  (*pfree)(void *, void *, void *);
    void  (*flush_cache)(void *, int);
    void *(*ve_get_phyaddr)(void *);
    void *(*ve_get_viraddr)(void *);
    void *(*cpu_get_phyaddr)(void *);
    void *(*cpu_get_viraddr)(void *);
    int   (*mem_set)(void *, int, size_t);
    int   (*mem_cpy)(void *, void *, size_t);
    int   (*mem_read)(void *, void *, size_t);
    int   (*mem_write)(void *, void *, size_t);
    int   (*setup)(void);
    int   (*shutdown)(void);
    unsigned int (*get_ve_addr_offset)(void);
    int   (*get_debug_info)(char *, int);
    void *(*get_vir_by_fd)(int);
    int   (*get_phy_by_fd)(int, void *);
    int   (*free_phy_by_fd)(int, unsigned long);
    int   (*get_fd_by_vir)(void *);
} ScMemOpsS;

/* Matches CedarC v1.3.0 vencoder.h VencBaseConfig exactly */
typedef struct {
    unsigned char   bEncH264Nalu;
    unsigned int    nInputWidth;
    unsigned int    nInputHeight;
    unsigned int    nDstWidth;
    unsigned int    nDstHeight;
    unsigned int    nStride;
    VENC_PIXEL_FMT  eInputFormat;
    void           *memops;     /* struct ScMemOpsS* — from MemAdapterGetOpsS() */
    void           *veOpsS;     /* VeOpsS*            — from GetVeOpsS(0) */
    void           *pVeOpsSelf; /* self handle         — pass NULL */
    unsigned char   bOnlyWbFlag;
    unsigned char   bLbcLossyComEnFlag2x;
    unsigned char   bLbcLossyComEnFlag2_5x;
    unsigned char   bIsVbvNoCache;
} VencBaseConfig;

typedef struct {
    unsigned char *pAddrVirY;
    unsigned char *pAddrVirC;
    unsigned char *pAddrPhyY;
    unsigned char *pAddrPhyC;
    int            nID;
    long long      nPts;
    long long      nDuration;
    int            bIsFirstFrame;
    int            bLastFrame;
    int            bEnableCorp;
    unsigned int   nShareBufFd;
    unsigned char  _pad[64]; /* absorb minor version layout differences */
} VencInputBuffer;

typedef struct {
    unsigned char *pData0;
    unsigned char *pData1;
    unsigned int   nSize0;
    unsigned int   nSize1;
    long long      nPts;
    int            bIsKeyFrame;
    int            nID;
    unsigned int   nTotalSize;
    unsigned char  _pad[32];
} VencOutputBuffer;

/* ── Function pointer types ─────────────────────────────────────────────── */

typedef VideoEncoder *(*fn_VideoEncCreate)(VENC_CODEC_TYPE);
typedef int  (*fn_VideoEncInit)(VideoEncoder *, VencBaseConfig *);
typedef void (*fn_VideoEncUnInit)(VideoEncoder *);
typedef void (*fn_VideoEncDestroy)(VideoEncoder *);
typedef struct {
    unsigned int nBufferNum;
    unsigned int nSizeY;
    unsigned int nSizeC;
} VencAllocateBufferParam;
typedef int  (*fn_AllocInputBuffer)(VideoEncoder *, VencAllocateBufferParam *);
typedef int  (*fn_GetOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_FlushCacheAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_ReturnOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_ReleaseAllocInputBuffer)(VideoEncoder *);
typedef int  (*fn_AddOneInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_VideoEncodeOneFrame)(VideoEncoder *);
typedef int  (*fn_ValidBitstreamFrameNum)(VideoEncoder *);
typedef int  (*fn_GetOneBitstreamFrame)(VideoEncoder *, VencOutputBuffer *);
typedef int  (*fn_FreeOneBitStreamFrame)(VideoEncoder *, VencOutputBuffer *);
typedef void *(*fn_GetVeOpsS_t)(int type);
typedef void *(*fn_GetOpsS)(void);

/* ── Library handles ────────────────────────────────────────────────────── */

static void *g_libVE, *g_libMem, *g_libvenc;

/* ── Function pointer globals ───────────────────────────────────────────── */

static fn_VideoEncCreate             p_VideoEncCreate;
static fn_VideoEncInit               p_VideoEncInit;
static fn_VideoEncUnInit             p_VideoEncUnInit;
static fn_VideoEncDestroy            p_VideoEncDestroy;
static fn_AllocInputBuffer           p_AllocInputBuffer;
static fn_GetOneAllocInputBuffer     p_GetOneAllocInputBuffer;
static fn_FlushCacheAllocInputBuffer p_FlushCacheAllocInputBuffer;
static fn_ReturnOneAllocInputBuffer  p_ReturnOneAllocInputBuffer;
static fn_ReleaseAllocInputBuffer    p_ReleaseAllocInputBuffer;
static fn_AddOneInputBuffer          p_AddOneInputBuffer;
static fn_VideoEncodeOneFrame        p_VideoEncodeOneFrame;
static fn_ValidBitstreamFrameNum     p_ValidBitstreamFrameNum;
static fn_GetOneBitstreamFrame       p_GetOneBitstreamFrame;
static fn_FreeOneBitStreamFrame      p_FreeOneBitStreamFrame;
static fn_GetOpsS                    p_MemAdapterGetOpsS;
static fn_GetVeOpsS_t                p_GetVeOpsS;

/* ── Helpers ────────────────────────────────────────────────────────────── */

#define LOG(fmt, ...) fprintf(stderr, "[cedar-probe] " fmt "\n", ##__VA_ARGS__)

#define LOADSYM(lib, var, name) do { \
    *(void **)(&(var)) = dlsym((lib), (name)); \
    if (!(var)) { LOG("dlsym(%s): %s", (name), dlerror()); return -1; } \
} while (0)

static int load_symbols(void)
{
    g_libVE = dlopen("libVE.so", RTLD_LAZY | RTLD_GLOBAL);
    if (!g_libVE) { LOG("dlopen(libVE.so): %s", dlerror()); return -1; }
    LOG("libVE.so loaded");

    g_libMem = dlopen("libMemAdapter.so", RTLD_LAZY | RTLD_GLOBAL);
    if (!g_libMem) { LOG("dlopen(libMemAdapter.so): %s", dlerror()); return -1; }
    LOG("libMemAdapter.so loaded");

    g_libvenc = dlopen("libvencoder.so", RTLD_LAZY | RTLD_GLOBAL);
    if (!g_libvenc) { LOG("dlopen(libvencoder.so): %s", dlerror()); return -1; }
    LOG("libvencoder.so loaded");

    LOADSYM(g_libVE,   p_GetVeOpsS,                 "GetVeOpsS");
    LOADSYM(g_libMem,  p_MemAdapterGetOpsS,          "MemAdapterGetOpsS");
    LOADSYM(g_libvenc, p_VideoEncCreate,             "VideoEncCreate");
    LOADSYM(g_libvenc, p_VideoEncInit,               "VideoEncInit");
    LOADSYM(g_libvenc, p_VideoEncUnInit,             "VideoEncUnInit");
    LOADSYM(g_libvenc, p_VideoEncDestroy,            "VideoEncDestroy");
    LOADSYM(g_libvenc, p_AllocInputBuffer,           "AllocInputBuffer");
    LOADSYM(g_libvenc, p_GetOneAllocInputBuffer,     "GetOneAllocInputBuffer");
    LOADSYM(g_libvenc, p_FlushCacheAllocInputBuffer, "FlushCacheAllocInputBuffer");
    LOADSYM(g_libvenc, p_ReturnOneAllocInputBuffer,  "ReturnOneAllocInputBuffer");
    LOADSYM(g_libvenc, p_ReleaseAllocInputBuffer,    "ReleaseAllocInputBuffer");
    LOADSYM(g_libvenc, p_AddOneInputBuffer,          "AddOneInputBuffer");
    LOADSYM(g_libvenc, p_VideoEncodeOneFrame,        "VideoEncodeOneFrame");
    LOADSYM(g_libvenc, p_ValidBitstreamFrameNum,     "ValidBitstreamFrameNum");
    LOADSYM(g_libvenc, p_GetOneBitstreamFrame,       "GetOneBitstreamFrame");
    LOADSYM(g_libvenc, p_FreeOneBitStreamFrame,      "FreeOneBitStreamFrame");

    LOG("all symbols resolved");
    return 0;
}

static void unload_libs(void)
{
    if (g_libvenc) { dlclose(g_libvenc); g_libvenc = NULL; }
    if (g_libMem)  { dlclose(g_libMem);  g_libMem  = NULL; }
    if (g_libVE)   { dlclose(g_libVE);   g_libVE   = NULL; }
}

/* Fill a stride*h NV12 buffer with a solid mid-grey frame. */
static void fill_nv12(unsigned char *y, unsigned char *uv,
                      unsigned int stride, unsigned int h)
{
    memset(y,  128, stride * h);
    memset(uv, 128, stride * h / 2);
}

int main(void)
{
    int ret       = 1;
    int enc_init  = 0;
    int buf_alloc = 0; /* AllocInputBuffer pool created */
    int buf_got   = 0; /* GetOneAllocInputBuffer in hand */
    int mem_open  = 0; /* CdcMemOpen2 called */
    ScMemOpsS       *memops = NULL;
    void            *veops  = NULL;
    VideoEncoder    *enc = NULL;
    VencInputBuffer  inbuf;
    VencOutputBuffer outbuf;
    size_t total = 0;

    memset(&inbuf,  0, sizeof inbuf);
    memset(&outbuf, 0, sizeof outbuf);

    /* 1. Verify /dev/cedar_dev is accessible */
    LOG("checking /dev/cedar_dev...");
    {
        int fd = open("/dev/cedar_dev", O_RDWR);
        if (fd < 0) { LOG("FAIL: %s", strerror(errno)); return 1; }
        close(fd);
    }
    LOG("/dev/cedar_dev ok");

    /* 2. Load libraries and resolve all symbols */
    if (load_symbols() != 0) goto done;

    /* 3. Resolve ops structs and open the memory adapter */
    veops  = p_GetVeOpsS(0);
    memops = (ScMemOpsS *)p_MemAdapterGetOpsS();
    if (!veops || !memops) { LOG("GetVeOpsS/MemAdapterGetOpsS FAIL"); goto done; }
    LOG("ops ok (veops=%p memops=%p)", veops, memops);
    LOG("memops->open = %p, open2 = %p", (void*)memops->open, (void*)memops->open2);
    {
        int r = memops->open();
        LOG("CdcMemOpen returned %d", r);
        if (r < 0) { LOG("CdcMemOpen FAIL"); goto done; }
    }
    mem_open = 1;
    LOG("CdcMemOpen ok");

    /* VeInitialize is called internally by VideoEncCreate; do not call it
     * explicitly or the reference count will be off and VeRelease will segfault. */

    /* 4. Create H.264 encoder handle */
    LOG("VideoEncCreate(H264)...");
    enc = p_VideoEncCreate(VENC_CODEC_H264);
    if (!enc) { LOG("VideoEncCreate FAIL"); goto done; }
    LOG("VideoEncCreate ok");

    /* 5. Configure encoder: 480x272 NV12 (272 = next multiple of 16 above 270) */
    LOG("VideoEncInit 480x272 NV12...");
    {
        VencBaseConfig cfg;
        memset(&cfg, 0, sizeof cfg);
        cfg.nInputWidth  = 480;
        cfg.nInputHeight = 272;
        cfg.nStride      = 480;
        cfg.nDstWidth    = 480;
        cfg.nDstHeight   = 272;
        cfg.eInputFormat = VENC_PIXEL_YUV420SP;
        cfg.memops       = memops;
        cfg.veOpsS       = veops;
        cfg.pVeOpsSelf   = NULL;
        if (p_VideoEncInit(enc, &cfg) != 0) { LOG("VideoEncInit FAIL"); goto done; }
    }
    enc_init = 1;
    LOG("VideoEncInit ok");

    /* 6. Allocate the ION-backed input buffer pool (must precede GetOneAllocInputBuffer) */
    LOG("AllocInputBuffer...");
    {
        VencAllocateBufferParam bufparam;
        memset(&bufparam, 0, sizeof bufparam);
        bufparam.nBufferNum = 1;
        bufparam.nSizeY     = 480 * 272;       /* Y plane: stride * height */
        bufparam.nSizeC     = 480 * 272 / 2;   /* UV plane: stride * height / 2 */
        if (p_AllocInputBuffer(enc, &bufparam) != 0) {
            LOG("AllocInputBuffer FAIL");
            goto done;
        }
    }
    buf_alloc = 1;
    LOG("AllocInputBuffer ok");

    /* 7. Get a pointer to the allocated buffer */
    LOG("GetOneAllocInputBuffer...");
    if (p_GetOneAllocInputBuffer(enc, &inbuf) != 0) {
        LOG("GetOneAllocInputBuffer FAIL");
        goto done;
    }
    buf_got = 1;
    LOG("GetOneAllocInputBuffer ok (Y=%p UV=%p)", inbuf.pAddrVirY, inbuf.pAddrVirC);

    /* 8. Fill buffer with synthetic frame and flush CPU cache */
    fill_nv12(inbuf.pAddrVirY, inbuf.pAddrVirC, 480, 272);
    inbuf.bIsFirstFrame = 1;
    inbuf.nPts          = 0;
    p_FlushCacheAllocInputBuffer(enc, &inbuf);

    /* 8. Submit frame and trigger encode */
    LOG("AddOneInputBuffer + VideoEncodeOneFrame...");
    if (p_AddOneInputBuffer(enc, &inbuf) != 0) { LOG("AddOneInputBuffer FAIL"); goto done; }
    if (p_VideoEncodeOneFrame(enc) != 0)        { LOG("VideoEncodeOneFrame FAIL"); goto done; }
    LOG("VideoEncodeOneFrame ok");

    /* 9. Poll for available bitstream output (up to ~1 s) */
    {
        int i;
        for (i = 0; i < 100 && p_ValidBitstreamFrameNum(enc) == 0; i++)
            usleep(10000);
        if (p_ValidBitstreamFrameNum(enc) == 0) {
            LOG("timed out waiting for bitstream");
            goto done;
        }
    }

    /* 10. Read NAL units from encoder output ring */
    LOG("GetOneBitstreamFrame...");
    if (p_GetOneBitstreamFrame(enc, &outbuf) != 0) {
        LOG("GetOneBitstreamFrame FAIL");
        goto done;
    }
    LOG("GetOneBitstreamFrame ok");

    /* 11. Write Annex B H.264 to file */
    {
        FILE *f = fopen("/tmp/cedar-probe.h264", "wb");
        if (!f) { LOG("fopen: %s", strerror(errno)); goto free_out; }
        if (outbuf.nSize0 > 0) { fwrite(outbuf.pData0, 1, outbuf.nSize0, f); total += outbuf.nSize0; }
        if (outbuf.nSize1 > 0) { fwrite(outbuf.pData1, 1, outbuf.nSize1, f); total += outbuf.nSize1; }
        fclose(f);
    }

    if (total > 0) {
        LOG("PASS: wrote /tmp/cedar-probe.h264 (%zu bytes)", total);
        ret = 0;
    } else {
        LOG("FAIL: bitstream was empty");
    }

free_out:
    p_FreeOneBitStreamFrame(enc, &outbuf);
done:
    if (buf_got)   p_ReturnOneAllocInputBuffer(enc, &inbuf);
    if (buf_alloc) p_ReleaseAllocInputBuffer(enc);
    if (enc_init)  p_VideoEncUnInit(enc);
    if (enc)       p_VideoEncDestroy(enc);
    if (mem_open && memops) memops->close();
    unload_libs();
    return ret;
}
