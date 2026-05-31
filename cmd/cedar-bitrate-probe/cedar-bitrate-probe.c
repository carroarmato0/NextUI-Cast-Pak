/* cmd/cedar-bitrate-probe/cedar-bitrate-probe.c
 *
 * Investigates Cedar VideoEncSetParameter behaviour by encoding a fixed
 * number of frames under different parameter configurations and reporting
 * the total output bytes for each.  Reads live content from /dev/fb0 so
 * results reflect real source material (run alongside fb-demo if desired).
 *
 * Build:
 *   aarch64-linux-gnu-gcc -O2 -o cedar-bitrate-probe \
 *       cmd/cedar-bitrate-probe/cedar-bitrate-probe.c -ldl
 *
 * Usage:
 *   cedar-bitrate-probe [frames]   (default: 60 frames = 2s at 30fps)
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

/* ── CedarC v1.3 types (same as cedar_encoder_linux_arm64.c) ──────────────── */
typedef enum { VENC_CODEC_H264 = 0 } VENC_CODEC_TYPE;
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
    unsigned int   nInputWidth, nInputHeight, nDstWidth, nDstHeight, nStride;
    VENC_PIXEL_FMT eInputFormat;
    void          *memops, *veOpsS, *pVeOpsSelf;
    unsigned char  bOnlyWbFlag, bLbcLossyComEnFlag2x, bLbcLossyComEnFlag2_5x, bIsVbvNoCache;
} VencBaseConfig;

typedef struct {
    unsigned char *pAddrVirY, *pAddrVirC, *pAddrPhyY, *pAddrPhyC;
    unsigned char *_phyUV, *_virY, *_virUV;
    int            nID, _pad;
    long long      nPts, nDuration;
    int            bIsFirstFrame, bLastFrame, bEnableCorp;
    unsigned int   nShareBufFd;
    unsigned char  _tail[256];
} VencInputBuffer;

typedef struct {
    int            _flags, _pad0[3], bIsKeyFrame;
    unsigned int   nTotalSize;
    int            nID, _align;
    unsigned char *pData0, *pData1;
    unsigned int   nSize0, nSize1;
    long long      nPts;
    unsigned char  _tail[32];
} VencOutputBuffer;

typedef struct { unsigned int nBufferNum, nSizeY, nSizeC; } VencAllocateBufferParam;

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
static fn_GetVeOpsS_t                p_GetVeOpsS;
static fn_GetOpsS                    p_MemAdapterGetOpsS;
static fn_SetParameter               p_VideoEncSetParameter;
static fn_SetParameter               p_VideoEncGetParameter;

#define LOADSYM(lib, var, name) do { \
    *(void **)(&(var)) = dlsym((lib), (name)); \
    if (!(var)) { fprintf(stderr, "dlsym(%s): %s\n", (name), dlerror()); return -1; } \
} while (0)

static int load_symbols(void) {
    g_libVE   = dlopen("libVE.so",         RTLD_LAZY | RTLD_GLOBAL);
    g_libMem  = dlopen("libMemAdapter.so", RTLD_LAZY | RTLD_GLOBAL);
    g_libvenc = dlopen("libvencoder.so",   RTLD_LAZY | RTLD_GLOBAL);
    if (!g_libVE || !g_libMem || !g_libvenc) { fprintf(stderr, "dlopen: %s\n", dlerror()); return -1; }
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
    *(void **)(&p_VideoEncGetParameter) = dlsym(g_libvenc, "VideoEncGetParameter");
    return 0;
}

static void unload_libs(void) {
    if (g_libvenc) { dlclose(g_libvenc); g_libvenc = NULL; }
    if (g_libMem)  { dlclose(g_libMem);  g_libMem  = NULL; }
    if (g_libVE)   { dlclose(g_libVE);   g_libVE   = NULL; }
}

/* ── rgb conversion ────────────────────────────────────────────────────────── */
static inline int clamp_y(int v)  { return v<16?16:v>235?235:v; }
static inline unsigned char rgb_to_y(int r,int g,int b) {
    return (unsigned char)clamp_y(((66*r+129*g+25*b+128)>>8)+16);
}
static inline int clamp_uv(int v) { return v<16?16:v>240?240:v; }
static void bgra_to_nv12(const uint8_t *fb, unsigned char *y_out,
                          unsigned char *uv_out, unsigned int w, unsigned int h) {
    unsigned int uv_i = 0;
    for (unsigned int row=0; row<h; row++) {
        for (unsigned int col=0; col<w; col++) {
            const uint8_t *p = &fb[(row*w+col)*4];
            int b=p[0],g=p[1],r=p[2];
            y_out[row*w+col] = rgb_to_y(r,g,b);
            if ((row&1)==0 && (col&1)==0) {
                uv_out[uv_i++] = (unsigned char)clamp_uv(((-38*r-74*g+112*b+128)>>8)+128);
                uv_out[uv_i++] = (unsigned char)clamp_uv(((112*r-94*g-18*b+128)>>8)+128);
            }
        }
    }
}

/* ── one encode trial ──────────────────────────────────────────────────────── */
typedef struct {
    const char *label;
    int         rc_mode_idx;   /* -1 = skip */
    int         rc_mode_val;
    int         bitrate_idx;   /* -1 = skip */
    int         bitrate_val;
    int         force_idr;     /* 1 = bIsFirstFrame=1 every frame */
} Trial;

static long run_trial(const Trial *t, int n_frames,
                      int fb_fd, uint8_t *fb_data, size_t fb_size,
                      unsigned int w, unsigned int h)
{
    long total_bytes  = -1;
    int  enc_init     = 0;
    int  buf_alloc    = 0;
    int  buf_got      = 0;
    int  mem_open     = 0;

    ScMemOpsS    *memops   = NULL;
    void         *veops    = NULL;
    VideoEncoder *enc      = NULL;
    VencInputBuffer  *inbuf    = (VencInputBuffer  *)calloc(1, 1024);
    VencInputBuffer  *reclaimed= (VencInputBuffer  *)calloc(1, 1024);
    VencOutputBuffer *outbuf   = (VencOutputBuffer *)calloc(1,  512);
    if (!inbuf || !reclaimed || !outbuf) goto done;

    veops  = p_GetVeOpsS(0);
    memops = (ScMemOpsS *)p_MemAdapterGetOpsS();
    if (!veops || !memops) goto done;
    if (memops->open() < 0) goto done;
    mem_open = 1;

    enc = p_VideoEncCreate(VENC_CODEC_H264);
    if (!enc) goto done;

    {
        VencBaseConfig bcfg;
        memset(&bcfg, 0, sizeof bcfg);
        bcfg.nInputWidth  = bcfg.nDstWidth  = bcfg.nStride = w;
        bcfg.nInputHeight = bcfg.nDstHeight = h;
        bcfg.eInputFormat = VENC_PIXEL_YUV420SP;
        bcfg.memops       = memops;
        bcfg.veOpsS       = veops;
        if (p_VideoEncInit(enc, &bcfg) != 0) goto done;
    }
    enc_init = 1;

    if (p_VideoEncSetParameter) {
        if (t->rc_mode_idx >= 0)
            p_VideoEncSetParameter(enc, t->rc_mode_idx, (void *)&t->rc_mode_val);
        if (t->bitrate_idx >= 0) {
            p_VideoEncSetParameter(enc, t->bitrate_idx, (void *)&t->bitrate_val);
            if (p_VideoEncGetParameter && t->bitrate_idx == 16) {
                int rb = -1;
                p_VideoEncGetParameter(enc, 16, &rb);
                fprintf(stderr, "[probe] idx16 set=%d readback=%d\n",
                        t->bitrate_val, rb);
            }
        }
    }

    {
        VencAllocateBufferParam bp = { .nBufferNum=1, .nSizeY=w*h, .nSizeC=w*h/2 };
        if (p_AllocInputBuffer(enc, &bp) != 0) goto done;
    }
    buf_alloc = 1;

    if (p_GetOneAllocInputBuffer(enc, inbuf) != 0) goto done;
    buf_got = 1;
    if (!inbuf->_virY || !inbuf->_virUV) goto done;

    total_bytes = 0;
    for (int i = 0; i < n_frames; i++) {
        pread(fb_fd, fb_data, fb_size, 0);
        bgra_to_nv12(fb_data, inbuf->_virY, inbuf->_virUV, w, h);
        inbuf->bIsFirstFrame = (i == 0 || t->force_idr);
        inbuf->nPts          = (long long)i * 33333;
        p_FlushCacheAllocInputBuffer(enc, inbuf);
        if (p_AddOneInputBuffer(enc, inbuf) != 0) { total_bytes = -1; goto done; }
        if (p_VideoEncodeOneFrame(enc) != 0) { total_bytes = -1; goto done; }

        while (p_ValidBitstreamFrameNum(enc) > 0) {
            memset(outbuf, 0, 512);
            if (p_GetOneBitstreamFrame(enc, outbuf) != 0) break;
            total_bytes += outbuf->nSize0 + outbuf->nSize1;
            p_FreeOneBitStreamFrame(enc, outbuf);
        }

        memset(reclaimed, 0, 1024);
        if (p_AlreadyUsedInputBuffer(enc, reclaimed) != 0 || !reclaimed->_virY) {
            total_bytes = -1; goto done;
        }
        p_ReturnOneAllocInputBuffer(enc, reclaimed);
        buf_got = 0;

        if (i < n_frames - 1) {
            if (p_GetOneAllocInputBuffer(enc, inbuf) != 0) { total_bytes = -1; goto done; }
            buf_got = 1;
        }
    }

done:
    if (buf_got && inbuf) p_ReturnOneAllocInputBuffer(enc, inbuf);
    if (buf_alloc)        p_ReleaseAllocInputBuffer(enc);
    if (enc_init)         p_VideoEncUnInit(enc);
    if (enc)              p_VideoEncDestroy(enc);
    if (mem_open)         memops->close();
    free(outbuf); free(reclaimed); free(inbuf);
    return total_bytes;
}

/* ── main ───────────────────────────────────────────────────────────────────── */
int main(int argc, char *argv[])
{
    int n_frames = argc > 1 ? atoi(argv[1]) : 60;
    if (n_frames <= 0) n_frames = 60;

    /* Suppress vendor library stderr noise for cleaner output */
    int devnull = open("/dev/null", O_WRONLY);
    int saved_stderr = dup(STDERR_FILENO);
    dup2(devnull, STDERR_FILENO);

    if (load_symbols() != 0) {
        dup2(saved_stderr, STDERR_FILENO);
        fprintf(stderr, "load_symbols failed\n");
        return 1;
    }

    /* Open framebuffer — kept open so each frame reads live content */
    unsigned int w = 1280, h = 720;
    size_t fb_size = (size_t)w * h * 4;
    uint8_t *fb_data = (uint8_t *)malloc(fb_size);
    if (!fb_data) { dup2(saved_stderr, STDERR_FILENO); fprintf(stderr, "malloc\n"); return 1; }
    int fb_fd = open("/dev/fb0", O_RDONLY);
    if (fb_fd < 0) {
        dup2(saved_stderr, STDERR_FILENO);
        fprintf(stderr, "fb0 open failed\n"); return 1;
    }

    /* Restore stderr for our own output */
    dup2(saved_stderr, STDERR_FILENO);
    close(saved_stderr);
    close(devnull);

    /*
     * Trial matrix — VENC_IndexType candidates from CedarC source:
     *   0 = VideoAvcType (H.264 profile/level)
     *   1 = Bitrate
     *   2 = Framerate
     *   3 = IntraRefresh
     *   4 = H264Nalu
     *   5 = BufferNum
     *   6 = RcMode  (0=CBR, 1=VBR, 2=FixQP)
     *   7 = VideoQPRange
     */
    const Trial trials[] = {
        /* label                      rc_idx rc_val  br_idx  br_val  force_idr */
        { "no params (default)",         -1,     0,     -1,        0,  0 },
        { "idx1=1500kbps (old)",         -1,     0,      1,  1500000,  0 },
        /* idx16 is the only GET-supported bitrate-like parameter */
        { "idx16=500kbps",               -1,     0,     16,   500000,  0 },
        { "idx16=1500kbps",              -1,     0,     16,  1500000,  0 },
        { "idx16=5000kbps",              -1,     0,     16,  5000000,  0 },
        { "idx16=8388608 (hw default)",  -1,     0,     16,  8388608,  0 },
        /* idx8 probed; try as rc-mode carrier */
        { "idx8=0 +idx16=1500k",          8,     0,     16,  1500000,  0 },
        { "idx8=1 +idx16=1500k",          8,     1,     16,  1500000,  0 },
        { "idx8=2 +idx16=1500k",          8,     2,     16,  1500000,  0 },
    };
    int n_trials = (int)(sizeof trials / sizeof trials[0]);

    /* ── Probe all indices 0..30 with GetParameter to find supported ones ── */
    {
        void *veops  = p_GetVeOpsS(0);
        ScMemOpsS *memops = (ScMemOpsS *)p_MemAdapterGetOpsS();
        if (veops && memops && memops->open() == 0) {
            VideoEncoder *enc = p_VideoEncCreate(VENC_CODEC_H264);
            if (enc) {
                VencBaseConfig bcfg;
                memset(&bcfg, 0, sizeof bcfg);
                bcfg.nInputWidth = bcfg.nDstWidth = bcfg.nStride = w;
                bcfg.nInputHeight = bcfg.nDstHeight = h;
                bcfg.eInputFormat = VENC_PIXEL_YUV420SP;
                bcfg.memops = memops; bcfg.veOpsS = veops;
                if (p_VideoEncInit(enc, &bcfg) == 0 && p_VideoEncGetParameter) {
                    printf("Probing VideoEncGetParameter indices 0..30:\n");
                    for (int idx = 0; idx <= 30; idx++) {
                        uint8_t buf[256];
                        memset(buf, 0, sizeof buf);
                        /* Redirect stderr briefly to capture "do not support" warn */
                        int tmp_fd = open("/dev/null", O_WRONLY);
                        int saved  = dup(STDERR_FILENO);
                        dup2(tmp_fd, STDERR_FILENO);
                        int r = p_VideoEncGetParameter(enc, idx, buf);
                        dup2(saved, STDERR_FILENO);
                        close(saved); close(tmp_fd);

                        /* Check if anything was written to buf (non-zero) */
                        int nonzero = 0;
                        for (int k = 0; k < 64; k++) if (buf[k]) { nonzero = k; break; }
                        if (r == 0 || nonzero) {
                            int v0, v1, v2, v3;
                            memcpy(&v0, buf+0,  4);
                            memcpy(&v1, buf+4,  4);
                            memcpy(&v2, buf+8,  4);
                            memcpy(&v3, buf+12, 4);
                            printf("  idx=%2d ret=%2d nonzero@%d  [0]=%d [1]=%d [2]=%d [3]=%d\n",
                                   idx, r, nonzero, v0, v1, v2, v3);
                        } else {
                            printf("  idx=%2d ret=%2d (unsupported/empty)\n", idx, r);
                        }
                    }
                }
                p_VideoEncUnInit(enc);
                p_VideoEncDestroy(enc);
            }
            memops->close();
        }
    }
    printf("\n");

    printf("Cedar bitrate investigation — %d frames each (live fb; run fb-demo alongside for motion)\n", n_frames);
    printf("%-48s  %8s  %8s\n", "Configuration", "bytes", "kbps");
    printf("%-48s  %8s  %8s\n", "---", "---", "---");

    double duration_s = (double)n_frames / 30.0;

    for (int i = 0; i < n_trials; i++) {
        long bytes = run_trial(&trials[i], n_frames, fb_fd, fb_data, fb_size, w, h);
        if (bytes < 0)
            printf("%-48s  %8s\n", trials[i].label, "FAIL");
        else
            printf("%-48s  %8ld  %8.1f\n",
                   trials[i].label, bytes,
                   (double)bytes * 8.0 / duration_s / 1000.0);
    }

    free(fb_data);
    close(fb_fd);
    unload_libs();
    return 0;
}
