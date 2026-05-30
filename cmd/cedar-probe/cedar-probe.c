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
#include <sys/mman.h>
#include <stdint.h>

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

/* VencInputBuffer layout for the TrimUI vendor CedarC build (AArch64).
 * The vendor version adds three extra pointer fields after pAddrPhyC compared
 * to the open-source CedarC v1.3.0 struct.  Empirically confirmed by raw struct
 * dump: pAddrVirY/VirC at offsets 0/8 are always NULL; the writable virtual
 * addresses live at offsets 40 (_virY) and 48 (_virUV).
 *
 *   offset  0: pAddrVirY   - NULL (not populated by this firmware)
 *   offset  8: pAddrVirC   - NULL
 *   offset 16: pAddrPhyY   - NULL
 *   offset 24: pAddrPhyC   - Y plane physical address (VE DMA)
 *   offset 32: _phyUV      - UV plane physical address (vendor extra)
 *   offset 40: _virY       - Y plane CPU-writable virtual address (vendor extra)
 *   offset 48: _virUV      - UV plane CPU-writable virtual address (vendor extra)
 *   offset 56: nID
 *   offset 64: nPts
 */
typedef struct {
    unsigned char *pAddrVirY;    /* 0  - NULL on TrimUI */
    unsigned char *pAddrVirC;    /* 8  - NULL */
    unsigned char *pAddrPhyY;    /* 16 - NULL */
    unsigned char *pAddrPhyC;    /* 24 - Y physical (VE DMA) */
    unsigned char *_phyUV;       /* 32 - UV physical (vendor extra) */
    unsigned char *_virY;        /* 40 - Y CPU virtual (vendor extra) */
    unsigned char *_virUV;       /* 48 - UV CPU virtual (vendor extra) */
    int            nID;          /* 56 */
    int            _pad;         /* 60 - alignment pad before long long */
    long long      nPts;         /* 64 */
    long long      nDuration;    /* 72 */
    int            bIsFirstFrame;/* 80 */
    int            bLastFrame;   /* 84 */
    int            bEnableCorp;  /* 88 */
    unsigned int   nShareBufFd;  /* 92 */
    unsigned char  _tail[64];    /* absorb any further vendor additions */
} VencInputBuffer;

/* VencOutputBuffer — vendor layout (AArch64, empirically determined).
 * Standard CedarC puts pData0 at offset 0; the vendor version shifts it.
 *
 *   offset  0: _flags (int) = 0
 *   offset  4-15: zeros/padding
 *   offset 16: bIsKeyFrame (int)
 *   offset 20: nTotalSize (int, total encoded bytes)
 *   offset 24: nID (int)
 *   offset 28: _align (int, padding)
 *   offset 32: pData0 (unsigned char *, first output region)
 *   offset 40: pData1 (unsigned char *, second region when ring wraps; NULL if no wrap)
 *   offset 48: nSize0 (unsigned int)
 *   offset 52: nSize1 (unsigned int)
 */
typedef struct {
    int            _flags;       /* 0  */
    int            _pad0[3];     /* 4  */
    int            bIsKeyFrame;  /* 16 */
    unsigned int   nTotalSize;   /* 20 */
    int            nID;          /* 24 */
    int            _align;       /* 28 */
    unsigned char *pData0;       /* 32 */
    unsigned char *pData1;       /* 40 */
    unsigned int   nSize0;       /* 48 */
    unsigned int   nSize1;       /* 52 */
    long long      nPts;         /* 56 */
    unsigned char  _tail[32];
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
typedef int  (*fn_VideoEncGetParameter)(VideoEncoder *, int, void *);

/* VencHeaderData — SPS/PPS buffer returned by VideoEncGetParameter */
typedef struct {
    unsigned char *pBuffer; /* VE bus address; needs ve_get_viraddr conversion */
    unsigned int   nLength;
} VencHeaderData;

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
static fn_VideoEncGetParameter       p_VideoEncGetParameter;

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

    /* Optional — may not exist in all vendor builds */
    *(void **)(&p_VideoEncGetParameter) = dlsym(g_libvenc, "VideoEncGetParameter");
    if (!p_VideoEncGetParameter) LOG("VideoEncGetParameter not found (will skip SPS/PPS)");

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

/* Write a block of AVCC-format data (4-byte big-endian length + NAL) as Annex B
 * (replace length prefix with 00 00 00 01 start code).
 * Returns 0 if the input looks like it is already Annex B, so the caller can
 * fall back to a raw write. */
static size_t write_avcc_as_annexb(FILE *f, const unsigned char *data, size_t len)
{
    static const unsigned char sc[4] = {0, 0, 0, 1};
    /* If first 4 bytes are already an Annex B start code, signal raw write. */
    if (len >= 4 && data[0] == 0 && data[1] == 0 && data[2] == 0 && data[3] == 1)
        return 0;
    size_t written = 0, off = 0;
    while (off + 4 <= len) {
        unsigned int nal = ((unsigned int)data[off]   << 24) |
                           ((unsigned int)data[off+1] << 16) |
                           ((unsigned int)data[off+2] <<  8) |
                            (unsigned int)data[off+3];
        if (nal == 0 || off + 4 + nal > len) break;
        fwrite(sc, 1, 4, f);
        fwrite(data + off + 4, 1, nal, f);
        written += 4 + nal;
        off     += 4 + nal;
    }
    return written;
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
    VencHeaderData   spspps;
    void            *spspps_vir = NULL; /* CPU virtual address of SPS/PPS, or NULL */
    size_t total = 0;

    memset(&inbuf,  0, sizeof inbuf);
    memset(&spspps, 0, sizeof spspps);

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

    /* 5. Configure encoder: 480x272 NV12 (272 = next multiple of 16 above 270).
     * bEncH264Nalu=0: raw Annex B output without inline SPS/PPS; we prepend
     * them manually in step 10 via VideoEncGetParameter(16). */
    LOG("VideoEncInit 480x272 NV12...");
    {
        VencBaseConfig cfg;
        memset(&cfg, 0, sizeof cfg);
        cfg.bEncH264Nalu = 0;
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
    LOG("GetOneAllocInputBuffer ok (virY=%p virUV=%p phyC=%p phyUV=%p)",
        inbuf._virY, inbuf._virUV, inbuf.pAddrPhyC, inbuf._phyUV);

    if (!inbuf._virY || !inbuf._virUV) {
        LOG("FAIL: buffer virtual addresses are NULL");
        goto done;
    }

    /* 8. Fill buffer with synthetic frame and flush CPU cache */
    fill_nv12(inbuf._virY, inbuf._virUV, 480, 272);
    inbuf.bIsFirstFrame = 1;
    inbuf.nPts          = 0;
    p_FlushCacheAllocInputBuffer(enc, &inbuf);

    /* 9. Submit frame and trigger encode */
    LOG("AddOneInputBuffer + VideoEncodeOneFrame...");
    if (p_AddOneInputBuffer(enc, &inbuf) != 0) { LOG("AddOneInputBuffer FAIL"); goto done; }
    if (p_VideoEncodeOneFrame(enc) != 0)        { LOG("VideoEncodeOneFrame FAIL"); goto done; }
    LOG("VideoEncodeOneFrame ok");

    /* 10. Retrieve SPS/PPS header.  Index 16 = VENC_IndexParamH264SPSPPS.
     *
     * VideoEncGetParameter(16) returns pBuffer = VE bus address (bit 32 set = VE SRAM).
     * That address is NOT accessible from CPU userspace on H618: the VE writes SPS/PPS
     * into its internal SRAM, which is not mapped into the process address space and
     * cannot be reached via /proc/self/maps or cedar_dev mmap.  We therefore use a
     * hardcoded SPS/PPS derived by bit-parsing the IDR output (65 88 80 40 01 ...):
     *   Baseline Profile Level 3.0, 480x272, log2_max_frame_num_minus4=0,
     *   poc_type=0, log2_max_poc_lsb_minus4=2, frame_mbs_only=1, direct_8x8=1. */
    if (p_VideoEncGetParameter) {
        VencHeaderData hdr;
        memset(&hdr, 0, sizeof hdr);
        if (p_VideoEncGetParameter(enc, 16, &hdr) == 0 &&
                hdr.pBuffer && hdr.nLength > 0) {

            unsigned long long ve_addr = (unsigned long long)(uintptr_t)hdr.pBuffer;
            LOG("SPSPPS pBuffer=0x%llx nLength=%u (VE SRAM, not CPU-accessible)",
                ve_addr, hdr.nLength);
            spspps = hdr;

            /* Hardcoded SPS/PPS — Annex B, Baseline Profile Level 3.0, 480x272.
             * PPS: CAVLC, pic_init_qp_minus26=0, chroma_qp_index_offset=0. */
            {
                static const unsigned char hardcoded_spspps[] = {
                    /* SPS */
                    0x00, 0x00, 0x00, 0x01,
                    0x67,                         /* nal_ref_idc=3, nal_unit_type=7 */
                    0x42, 0x40, 0x1e,             /* Baseline, constraint_set1, Level 3.0 */
                    0xed, 0x03, 0xc1, 0x1c, 0x80, /* RBSP: 480x272, mbs_only, direct_8x8 */
                    /* PPS */
                    0x00, 0x00, 0x00, 0x01,
                    0x68,                         /* nal_ref_idc=3, nal_unit_type=8 */
                    0xce, 0x3c, 0x80,             /* RBSP: CAVLC, deblock=1 */
                };
                spspps_vir = (void *)hardcoded_spspps;
                spspps.nLength = sizeof hardcoded_spspps;
                LOG("using hardcoded SPS/PPS (%u bytes)", spspps.nLength);
            }
        } else {
            LOG("VideoEncGetParameter(16) failed");
        }
    }

    /* 11. Poll for available bitstream output (up to ~1 s) */
    {
        int i;
        for (i = 0; i < 100 && p_ValidBitstreamFrameNum(enc) == 0; i++)
            usleep(10000);
        if (p_ValidBitstreamFrameNum(enc) == 0) {
            LOG("timed out waiting for bitstream");
            goto done;
        }
    }

    /* 12. Drain all available output frames into /tmp/cedar-probe.h264.
     * SPS/PPS is prepended manually on the first IDR frame (step 10). */
    {
        FILE *f = fopen("/tmp/cedar-probe.h264", "wb");
        if (!f) { LOG("fopen: %s", strerror(errno)); goto done; }

        int fc = 0;
        while (p_ValidBitstreamFrameNum(enc) > 0) {
            VencOutputBuffer outbuf;
            memset(&outbuf, 0, sizeof outbuf);
            if (p_GetOneBitstreamFrame(enc, &outbuf) != 0) {
                LOG("GetOneBitstreamFrame FAIL on frame %d", fc);
                fclose(f);
                goto done;
            }
            LOG("frame %d: keyframe=%d totalSize=%u data0=%p",
                fc, outbuf.bIsKeyFrame, outbuf.nTotalSize, outbuf.pData0);

            /* On the first IDR frame, prepend SPS/PPS in Annex B. */
            if (fc == 0 && spspps_vir && spspps.nLength > 0) {
                unsigned char *d = (unsigned char *)spspps_vir;
                size_t n;
                /* If already Annex B (starts with 00 00 00 01), write raw.
                 * Otherwise convert from AVCC length-prefixed format. */
                if (spspps.nLength >= 4 &&
                    d[0]==0 && d[1]==0 && d[2]==0 && d[3]==1) {
                    fwrite(d, 1, spspps.nLength, f);
                    n = spspps.nLength;
                } else {
                    n = write_avcc_as_annexb(f, d, spspps.nLength);
                }
                LOG("wrote SPS/PPS (%zu bytes)", n);
                total += n;
            }

            /* Write the IDR frame.  Use nTotalSize for the full contiguous
             * bitstream; convert AVCC → Annex B, or write raw if already
             * Annex B (00 00 00 01 prefix). */
            if (outbuf.pData0 && outbuf.nTotalSize > 0) {
                size_t n = write_avcc_as_annexb(f, outbuf.pData0, outbuf.nTotalSize);
                if (n == 0) {
                    fwrite(outbuf.pData0, 1, outbuf.nTotalSize, f);
                    n = outbuf.nTotalSize;
                }
                total += n;
            }
            p_FreeOneBitStreamFrame(enc, &outbuf);
            fc++;
        }
        fclose(f);
        LOG("drained %d frame(s)", fc);
    }

    if (total > 0) {
        LOG("PASS: wrote /tmp/cedar-probe.h264 (%zu bytes)", total);
        ret = 0;
    } else {
        LOG("FAIL: bitstream was empty");
    }

done:
    if (buf_got)   p_ReturnOneAllocInputBuffer(enc, &inbuf);
    if (buf_alloc) p_ReleaseAllocInputBuffer(enc);
    if (enc_init)  p_VideoEncUnInit(enc);
    if (enc)       p_VideoEncDestroy(enc);
    if (mem_open && memops) memops->close();
    unload_libs();
    return ret;
}
