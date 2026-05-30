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
typedef enum { VENC_PIXEL_YUV420SP = 1 } VENC_PIXEL_FMT;
typedef struct VideoEncoder VideoEncoder;

typedef struct {
    unsigned int   nInputWidth;
    unsigned int   nInputHeight;
    unsigned int   nStride;
    unsigned int   nDstWidth;
    unsigned int   nDstHeight;
    VENC_PIXEL_FMT eInputFormat;
    void          *pMemops;
    void          *pVeops;
    int            nSocId;
    unsigned int   nDspBufNum;
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
typedef int  (*fn_GetOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_FlushCacheAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_ReturnOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_ReleaseAllocInputBuffer)(VideoEncoder *);
typedef int  (*fn_AddOneInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef int  (*fn_VideoEncodeOneFrame)(VideoEncoder *);
typedef int  (*fn_ValidBitstreamFrameNum)(VideoEncoder *);
typedef int  (*fn_GetOneBitstreamFrame)(VideoEncoder *, VencOutputBuffer *);
typedef int  (*fn_FreeOneBitStreamFrame)(VideoEncoder *, VencOutputBuffer *);
typedef void *(*fn_GetOpsS)(void);
typedef int  (*fn_VeInitialize)(void);
typedef void (*fn_VeRelease)(void);

/* ── Library handles ────────────────────────────────────────────────────── */

static void *g_libVE, *g_libMem, *g_libvenc;

/* ── Function pointer globals ───────────────────────────────────────────── */

static fn_VideoEncCreate             p_VideoEncCreate;
static fn_VideoEncInit               p_VideoEncInit;
static fn_VideoEncUnInit             p_VideoEncUnInit;
static fn_VideoEncDestroy            p_VideoEncDestroy;
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
static fn_GetOpsS                    p_GetVeOpsS;
static fn_VeInitialize               p_VeInitialize;
static fn_VeRelease                  p_VeRelease;

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

    LOADSYM(g_libVE,   p_VeInitialize,              "VeInitialize");
    LOADSYM(g_libVE,   p_VeRelease,                 "VeRelease");
    LOADSYM(g_libVE,   p_GetVeOpsS,                 "GetVeOpsS");
    LOADSYM(g_libMem,  p_MemAdapterGetOpsS,          "MemAdapterGetOpsS");
    LOADSYM(g_libvenc, p_VideoEncCreate,             "VideoEncCreate");
    LOADSYM(g_libvenc, p_VideoEncInit,               "VideoEncInit");
    LOADSYM(g_libvenc, p_VideoEncUnInit,             "VideoEncUnInit");
    LOADSYM(g_libvenc, p_VideoEncDestroy,            "VideoEncDestroy");
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
    int ve_init   = 0;
    int enc_init  = 0;
    int buf_alloc = 0;
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

    /* 3. Initialise VE hardware engine */
    LOG("VeInitialize...");
    {
        int ve_ret = p_VeInitialize();
        LOG("VeInitialize returned %d (0x%x)", ve_ret, (unsigned)ve_ret);
        if (ve_ret < 0) { LOG("VeInitialize FAIL"); goto done; }
    }
    ve_init = 1;
    LOG("VeInitialize ok");

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
        cfg.pMemops      = p_MemAdapterGetOpsS();
        cfg.pVeops       = p_GetVeOpsS();
        if (p_VideoEncInit(enc, &cfg) != 0) { LOG("VideoEncInit FAIL"); goto done; }
    }
    enc_init = 1;
    LOG("VideoEncInit ok");

    /* 6. Allocate an ION-backed input buffer */
    LOG("GetOneAllocInputBuffer...");
    if (p_GetOneAllocInputBuffer(enc, &inbuf) != 0) {
        LOG("GetOneAllocInputBuffer FAIL");
        goto done;
    }
    buf_alloc = 1;
    LOG("GetOneAllocInputBuffer ok (Y=%p UV=%p)", inbuf.pAddrVirY, inbuf.pAddrVirC);

    /* 7. Fill buffer with synthetic frame and flush CPU cache */
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
    if (buf_alloc) { p_ReturnOneAllocInputBuffer(enc, &inbuf); p_ReleaseAllocInputBuffer(enc); }
    if (enc_init)  p_VideoEncUnInit(enc);
    if (enc)       p_VideoEncDestroy(enc);
    if (ve_init)   p_VeRelease();
    unload_libs();
    return ret;
}
