/* internal/stream/cedar_encoder_linux_arm64.c
 * Cedar H.264 hardware encode loop for Allwinner H618 (TrimUI devices).
 * Compiled only on linux/arm64 via CGO build constraints on the Go side.
 */
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <dlfcn.h>
#include <time.h>

/* ── cedar_cfg_t ────────────────────────────────────────────────────────── */

typedef struct {
    unsigned int   width;
    unsigned int   height;
    unsigned int   fps;
    unsigned int   gop;
    unsigned int   bitrate_kbps;
    uintptr_t      writer_handle;   /* cgo.Handle value; passed to cedar_write_go */
    int            bpp;             /* framebuffer bits-per-pixel (16 or 32) */
} cedar_cfg_t;

/* Go callback — defined in cedar_encoder.go with //export */
extern int cedar_write_go(uintptr_t handle, const void *data, int n);

/* ── CedarC v1.3.0 types (same layout as cedar-probe) ───────────────────── */

typedef enum { VENC_CODEC_H264     = 0 } VENC_CODEC_TYPE;
typedef enum { VENC_PIXEL_YUV420SP = 0 } VENC_PIXEL_FMT;
typedef struct VideoEncoder VideoEncoder;

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

typedef struct {
    unsigned char  bEncH264Nalu;
    unsigned int   nInputWidth;
    unsigned int   nInputHeight;
    unsigned int   nDstWidth;
    unsigned int   nDstHeight;
    unsigned int   nStride;
    VENC_PIXEL_FMT eInputFormat;
    void          *memops;
    void          *veOpsS;
    void          *pVeOpsSelf;
    unsigned char  bOnlyWbFlag;
    unsigned char  bLbcLossyComEnFlag2x;
    unsigned char  bLbcLossyComEnFlag2_5x;
    unsigned char  bIsVbvNoCache;
} VencBaseConfig;

typedef struct {
    unsigned char *pAddrVirY;
    unsigned char *pAddrVirC;
    unsigned char *pAddrPhyY;
    unsigned char *pAddrPhyC;
    unsigned char *_phyUV;
    unsigned char *_virY;
    unsigned char *_virUV;
    int            nID;
    int            _pad;
    long long      nPts;
    long long      nDuration;
    int            bIsFirstFrame;
    int            bLastFrame;
    int            bEnableCorp;
    unsigned int   nShareBufFd;
    unsigned char  _tail[256]; /* enlarged to absorb library writes past known fields */
} VencInputBuffer;

typedef struct {
    int            _flags;
    int            _pad0[3];
    int            bIsKeyFrame;
    unsigned int   nTotalSize;
    int            nID;
    int            _align;
    unsigned char *pData0;
    unsigned char *pData1;
    unsigned int   nSize0;
    unsigned int   nSize1;
    long long      nPts;
    unsigned char  _tail[32];
} VencOutputBuffer;

typedef struct {
    unsigned int nBufferNum;
    unsigned int nSizeY;
    unsigned int nSizeC;
} VencAllocateBufferParam;

/* ── Function pointer types ─────────────────────────────────────────────── */

typedef VideoEncoder *(*fn_VideoEncCreate)(VENC_CODEC_TYPE);
typedef int  (*fn_VideoEncInit)(VideoEncoder *, VencBaseConfig *);
typedef void (*fn_VideoEncUnInit)(VideoEncoder *);
typedef void (*fn_VideoEncDestroy)(VideoEncoder *);
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
typedef int  (*fn_AlreadyUsedInputBuffer)(VideoEncoder *, VencInputBuffer *);
typedef void *(*fn_GetVeOpsS_t)(int);
typedef void *(*fn_GetOpsS)(void);
typedef int  (*fn_SetParameter)(VideoEncoder *, int, void *);

/* ── Globals ─────────────────────────────────────────────────────────────── */

static void *g_libVE, *g_libMem, *g_libvenc;

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
static fn_AlreadyUsedInputBuffer     p_AlreadyUsedInputBuffer;
static fn_GetVeOpsS_t               p_GetVeOpsS;
static fn_GetOpsS                    p_MemAdapterGetOpsS;
static fn_SetParameter               p_VideoEncSetParameter;

#define LOG(fmt, ...) fprintf(stderr, "[cedar_encoder] " fmt "\n", ##__VA_ARGS__)

#define LOADSYM(lib, var, name) do { \
    *(void **)(&(var)) = dlsym((lib), (name)); \
    if (!(var)) { LOG("dlsym(%s): %s", (name), dlerror()); return -1; } \
} while (0)

/* ── Library loading ────────────────────────────────────────────────────── */

static int load_symbols(void)
{
    g_libVE = dlopen("libVE.so", RTLD_LAZY | RTLD_GLOBAL);
    if (!g_libVE) { LOG("dlopen(libVE.so): %s", dlerror()); return -1; }

    g_libMem = dlopen("libMemAdapter.so", RTLD_LAZY | RTLD_GLOBAL);
    if (!g_libMem) { LOG("dlopen(libMemAdapter.so): %s", dlerror()); return -1; }

    g_libvenc = dlopen("libvencoder.so", RTLD_LAZY | RTLD_GLOBAL);
    if (!g_libvenc) { LOG("dlopen(libvencoder.so): %s", dlerror()); return -1; }

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
    LOADSYM(g_libvenc, p_AlreadyUsedInputBuffer,     "AlreadyUsedInputBuffer");

    *(void **)(&p_VideoEncSetParameter) = dlsym(g_libvenc, "VideoEncSetParameter");

    return 0;
}

static void unload_libs(void)
{
    if (g_libvenc) { dlclose(g_libvenc); g_libvenc = NULL; }
    if (g_libMem)  { dlclose(g_libMem);  g_libMem  = NULL; }
    if (g_libVE)   { dlclose(g_libVE);   g_libVE   = NULL; }
}

/* ── Pixel conversion: framebuffer → NV12 ──────────────────────────────── */

static inline int clamp_y(int v)  { return v < 16  ? 16  : v > 235 ? 235 : v; }
static inline int clamp_uv(int v) { return v < 16  ? 16  : v > 240 ? 240 : v; }

static inline unsigned char rgb_to_y(int r, int g, int b)
{
    return (unsigned char)clamp_y(((66*r + 129*g + 25*b + 128) >> 8) + 16);
}

static inline void rgb_to_uv(int r, int g, int b,
                              unsigned char *out_cb, unsigned char *out_cr)
{
    *out_cb = (unsigned char)clamp_uv(((-38*r - 74*g + 112*b + 128) >> 8) + 128);
    *out_cr = (unsigned char)clamp_uv(((112*r - 94*g -  18*b + 128) >> 8) + 128);
}

static void rgb565_to_nv12(const uint16_t *fb,
                            unsigned char *y_out, unsigned char *uv_out,
                            unsigned int w, unsigned int h)
{
    unsigned int uv_i = 0;
    for (unsigned int row = 0; row < h; row++) {
        for (unsigned int col = 0; col < w; col++) {
            uint16_t px = fb[row * w + col];
            int r = ((px >> 11) & 0x1f) * 255 / 31;
            int g = ((px >>  5) & 0x3f) * 255 / 63;
            int b = ( px        & 0x1f) * 255 / 31;
            y_out[row * w + col] = rgb_to_y(r, g, b);

            if ((row & 1) == 0 && (col & 1) == 0) {
                unsigned char cb, cr;
                rgb_to_uv(r, g, b, &cb, &cr);
                uv_out[uv_i++] = cb;
                uv_out[uv_i++] = cr;
            }
        }
    }
}

static void bgra_to_nv12(const uint8_t *fb,
                          unsigned char *y_out, unsigned char *uv_out,
                          unsigned int w, unsigned int h)
{
    unsigned int uv_i = 0;
    for (unsigned int row = 0; row < h; row++) {
        for (unsigned int col = 0; col < w; col++) {
            const uint8_t *p = &fb[(row * w + col) * 4];
            int b = p[0], g = p[1], r = p[2];
            y_out[row * w + col] = rgb_to_y(r, g, b);

            if ((row & 1) == 0 && (col & 1) == 0) {
                unsigned char cb, cr;
                rgb_to_uv(r, g, b, &cb, &cr);
                uv_out[uv_i++] = cb;
                uv_out[uv_i++] = cr;
            }
        }
    }
}

/* ── Frame timing helper ─────────────────────────────────────────────────── */

static void deadline_advance(struct timespec *ts, long frame_ns)
{
    ts->tv_nsec += frame_ns;
    if (ts->tv_nsec >= 1000000000L) {
        ts->tv_nsec -= 1000000000L;
        ts->tv_sec++;
    }
}

/* ── cedar_run: main encode loop ─────────────────────────────────────────── */

int cedar_run(cedar_cfg_t *cfg, volatile int *stop_flag)
{
    LOG("cedar_run entered w=%u h=%u bpp=%d fps=%u", cfg->width, cfg->height, cfg->bpp, cfg->fps);
    int ret        = -1;
    int enc_init   = 0;
    int buf_alloc  = 0;
    int buf_got    = 0;
    int mem_open   = 0;
    int first_frame = 1;

    ScMemOpsS    *memops   = NULL;
    void         *veops    = NULL;
    VideoEncoder *enc      = NULL;
    /* Heap-allocated so vendor library writes past sizeof() stay in the heap
     * and cannot corrupt the CGO wrapper's saved return address. */
    VencInputBuffer  *inbuf    = NULL;
    VencInputBuffer  *reclaimed = NULL;
    VencOutputBuffer *outbuf   = NULL;

    unsigned int w = cfg->width;
    unsigned int h = cfg->height;
    size_t fb_size  = w * h * (size_t)(cfg->bpp / 8);
    uint8_t *fb_buf = NULL;

    int fb_fd = -1;

    /* ── Open framebuffer ── */
    fb_fd = open("/dev/fb0", O_RDONLY);
    if (fb_fd < 0) { LOG("open(/dev/fb0): %s", strerror(errno)); goto done; }
    LOG("fb0 opened, fb_size=%zu", fb_size);

    fb_buf = (uint8_t *)malloc(fb_size);
    if (!fb_buf) { LOG("malloc fb_buf: %s", strerror(errno)); goto done; }
    LOG("fb_buf allocated");

    /* ── Load Cedar libs ── */
    if (load_symbols() != 0) goto done;
    LOG("symbols loaded");

    /* ── Init ops structs and memory adapter ── */
    veops  = p_GetVeOpsS(0);
    memops = (ScMemOpsS *)p_MemAdapterGetOpsS();
    if (!veops || !memops) { LOG("GetVeOpsS/MemAdapterGetOpsS FAIL"); goto done; }
    LOG("ops ok veops=%p memops=%p", veops, memops);
    if (memops->open() < 0) { LOG("CdcMemOpen FAIL"); goto done; }
    mem_open = 1;
    LOG("mem open");

    /* VeInitialize is called internally by VideoEncCreate — do not call it
     * explicitly or the refcount will be off and VeRelease will segfault. */

    /* ── Create encoder ── */
    enc = p_VideoEncCreate(VENC_CODEC_H264);
    if (!enc) { LOG("VideoEncCreate FAIL"); goto done; }
    LOG("encoder created enc=%p", enc);

    /* ── Init encoder ── */
    {
        VencBaseConfig bcfg;
        memset(&bcfg, 0, sizeof bcfg);
        bcfg.bEncH264Nalu  = 0;   /* Annex B output; SPS/PPS prepended by Go */
        bcfg.nInputWidth   = w;
        bcfg.nInputHeight  = h;
        bcfg.nDstWidth     = w;
        bcfg.nDstHeight    = h;
        bcfg.nStride       = w;
        bcfg.eInputFormat  = VENC_PIXEL_YUV420SP;
        bcfg.memops        = memops;
        bcfg.veOpsS        = veops;
        bcfg.pVeOpsSelf    = NULL;
        if (p_VideoEncInit(enc, &bcfg) != 0) { LOG("VideoEncInit FAIL"); goto done; }
    }
    enc_init = 1;
    LOG("encoder init ok");

    /* ── Bitrate configuration note ──
     * Probing revealed that H264SetParameterVer2 in this libvencoder build
     * returns -1 ("do not support this indexType") for ALL indices 0–7
     * (including index 1 = VENC_IndexParamBitrate).  VideoEncGetParameter
     * only succeeds for indices 8, 14 (resolution), and 16 (which reads back
     * 8388608 = 8 Mbps regardless of any SetParameter call).
     * The encoder operates at a hardware-determined VBR with an ~8 Mbps ceiling;
     * actual output bitrate is set by content complexity (a static game menu
     * produces ~10 kbps; busy game scenes produce hundreds of kbps).
     * cfg->bitrate_kbps is accepted but currently has no effect. */

    /* ── Allocate ION input buffer pool ── */
    {
        VencAllocateBufferParam bp;
        memset(&bp, 0, sizeof bp);
        bp.nBufferNum = 1;
        bp.nSizeY     = w * h;
        bp.nSizeC     = w * h / 2;
        if (p_AllocInputBuffer(enc, &bp) != 0) { LOG("AllocInputBuffer FAIL"); goto done; }
    }
    buf_alloc = 1;
    LOG("input buffer allocated");

    /* Allocate buffer descriptors on the heap.  The vendor library writes more
     * bytes than sizeof() into all three of these structs; keeping them off the
     * stack prevents any overflow from corrupting the CGO wrapper's saved return
     * address above cedar_run's frame.  512–1024 bytes is comfortably larger
     * than any known vendor extension of each struct. */
    inbuf     = (VencInputBuffer  *)calloc(1, 1024);
    reclaimed = (VencInputBuffer  *)calloc(1, 1024);
    outbuf    = (VencOutputBuffer *)calloc(1,  512);
    if (!inbuf || !reclaimed || !outbuf) { LOG("calloc buf FAIL"); goto done; }

    /* Get one input buffer from the free pool; subsequent frames reclaim it
     * via AlreadyUsedInputBuffer after each VideoEncodeOneFrame. */
    if (p_GetOneAllocInputBuffer(enc, inbuf) != 0) {
        LOG("GetOneAllocInputBuffer FAIL"); goto done;
    }
    buf_got = 1;

    if (!inbuf->_virY || !inbuf->_virUV) {
        LOG("GetOneAllocInputBuffer: NULL virtual addresses"); goto done;
    }
    LOG("got input buffer virY=%p virUV=%p", inbuf->_virY, inbuf->_virUV);

    /* ── Encode loop ── */
    struct timespec next;
    clock_gettime(CLOCK_MONOTONIC, &next);
    long frame_ns = 1000000000L / (long)cfg->fps;
    LOG("entering encode loop frame_ns=%ld", frame_ns);

    int frame_count = 0;
    while (!*stop_flag) {
        /* Capture framebuffer frame */
        ssize_t n = pread(fb_fd, fb_buf, fb_size, 0);
        if (n != (ssize_t)fb_size) {
            LOG("pread fb0: expected %zu got %zd", fb_size, n);
            goto done;
        }

        /* Convert framebuffer to NV12 in Cedar input buffer */
        if (cfg->bpp == 16) {
            rgb565_to_nv12((const uint16_t *)fb_buf,
                           inbuf->_virY, inbuf->_virUV, w, h);
        } else {
            bgra_to_nv12(fb_buf, inbuf->_virY, inbuf->_virUV, w, h);
        }

        inbuf->bIsFirstFrame = first_frame;
        inbuf->nPts          = 0;
        first_frame          = 0;

        p_FlushCacheAllocInputBuffer(enc, inbuf);

        if (p_AddOneInputBuffer(enc, inbuf) != 0) {
            LOG("AddOneInputBuffer FAIL"); goto done;
        }
        if (p_VideoEncodeOneFrame(enc) != 0) {
            LOG("VideoEncodeOneFrame FAIL"); goto done;
        }

        /* Drain and forward output bitstream.
         * VideoEncodeOneFrame is async; guard with ValidBitstreamFrameNum so we
         * never call GetOneBitstreamFrame before the hardware has produced output. */
        while (p_ValidBitstreamFrameNum(enc) > 0) {
            memset(outbuf, 0, 512);
            if (p_GetOneBitstreamFrame(enc, outbuf) != 0) break;
            if (outbuf->pData0 && outbuf->nSize0 > 0)
                cedar_write_go(cfg->writer_handle, outbuf->pData0, (int)outbuf->nSize0);
            if (outbuf->pData1 && outbuf->nSize1 > 0)
                cedar_write_go(cfg->writer_handle, outbuf->pData1, (int)outbuf->nSize1);
            p_FreeOneBitStreamFrame(enc, outbuf);
        }

        /* Reclaim the just-encoded input buffer from the encoder's "used" queue.
         * Without this the encoder's pending counter overflows after ~4 frames
         * and AddOneInputBuffer starts failing.
         * Pass reclaimed directly to ReturnOne to avoid a partial memcpy that
         * would lose any vendor tail bytes written past sizeof(VencInputBuffer). */
        memset(reclaimed, 0, 1024);
        if (p_AlreadyUsedInputBuffer(enc, reclaimed) != 0 || !reclaimed->_virY) {
            LOG("AlreadyUsedInputBuffer FAIL"); goto done;
        }
        p_ReturnOneAllocInputBuffer(enc, reclaimed);
        buf_got = 0;

        if (p_GetOneAllocInputBuffer(enc, inbuf) != 0) {
            LOG("GetOneAllocInputBuffer FAIL frame=%d", frame_count); goto done;
        }
        buf_got = 1;

        frame_count++;
        /* Pace to target fps */
        deadline_advance(&next, frame_ns);
        clock_nanosleep(CLOCK_MONOTONIC, TIMER_ABSTIME, &next, NULL);
    }

    ret = 0;
    LOG("encode loop done frames=%d", frame_count);

done:
    if (buf_got && inbuf) p_ReturnOneAllocInputBuffer(enc, inbuf);
    if (buf_alloc)        p_ReleaseAllocInputBuffer(enc);
    if (enc_init)         p_VideoEncUnInit(enc);
    if (enc)              p_VideoEncDestroy(enc);
    if (mem_open)         memops->close();
    unload_libs();
    free(outbuf);
    free(reclaimed);
    free(inbuf);
    free(fb_buf);
    if (fb_fd >= 0) close(fb_fd);
    return ret;
}

/* ── cedar_probe: lightweight probe used by NewCedarEncoder ─────────────── */

int cedar_probe(void)
{
    int fd = open("/dev/cedar_dev", O_RDWR);
    if (fd < 0) return -1;
    close(fd);

    void *h = dlopen("libvencoder.so", RTLD_LAZY);
    if (!h) return -1;
    dlclose(h);
    return 0;
}
