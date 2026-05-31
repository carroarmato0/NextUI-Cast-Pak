# Cedar Hardware H.264 Encoder — Developer Reference

Hardware H.264 encoding on TrimUI devices (Allwinner H618 SoC) via the
CedarC video engine library stack.  Everything in this document is based on
empirical reverse-engineering — no official Allwinner SDK or public headers
exist for this build.

---

## 1. Hardware and Platform

| | |
|---|---|
| SoC | Allwinner H618 |
| Video Engine | Cedar VE (dedicated encode/decode block) |
| Supported devices | tg5040 (TrimUI Brick, Smart Pro), tg5050 (Smart Pro S) |
| Kernel | 4.9.191 (vendor BSP) |
| Max encode resolution | 1280×720 tested; hardware ceiling likely 1080p |
| Max frame rate | 30 fps at 1280×720 (no CPU cost) |
| Output format | H.264 Annex-B NAL units |
| Bitrate model | Hardware VBR, ~8 Mbps ceiling, content-driven |

---

## 2. Library Stack

All libraries live in `/usr/lib/` and are loaded at runtime via `dlopen`.
There are no development headers on the device.

```
Application
    │
    ├── libvencoder.so          Public API — VideoEncCreate/Init/Encode/Destroy
    │       │
    │       ├── libvenc_h264.so     H.264 codec plugin (loaded by libvencoder)
    │       ├── libvenc_h265.so     H.265 codec plugin
    │       ├── libvenc_jpeg.so     MJPEG codec plugin
    │       ├── libvenc_base.so     Shared encoder infrastructure
    │       └── libvenc_common.so   Common utilities
    │
    ├── libVE.so                Video Engine hardware abstraction
    │       │
    │       └── GetVeOpsS()     Returns opaque VE operations struct
    │
    └── libMemAdapter.so        ION memory allocator for DMA-safe buffers
            │
            └── MemAdapterGetOpsS()  Returns opaque memory ops struct
```

### 2.1 Why three libraries?

- **libVE.so** opens `/dev/cedar_dev` or the equivalent VE driver node and
  manages hardware register access.  You must load it before libvencoder.
- **libMemAdapter.so** allocates ION (contiguous physical memory) buffers.
  The Cedar VE DMA engine requires physically-contiguous NV12 input buffers —
  regular `malloc` memory will not work.
- **libvencoder.so** is the public API surface.  Internally it dlopen's the
  appropriate codec plugin (`libvenc_h264.so`) based on the codec type passed
  to `VideoEncCreate`.

---

## 3. Complete Function Reference

All functions are resolved via `dlsym` at runtime.  None are declared in
any installed header.

### 3.1 VE / Memory bootstrap (libVE.so, libMemAdapter.so)

```c
// Returns an opaque pointer to VE hardware operations.
// Pass 0 for the default VE instance.
// Required: pass to VencBaseConfig.veOpsS before VideoEncInit.
void *GetVeOpsS(int ve_index);

// Returns an opaque pointer to memory allocator operations (ScMemOpsS).
// Required: pass to VencBaseConfig.memops before VideoEncInit.
void *MemAdapterGetOpsS(void);
```

### 3.2 Encoder lifecycle (libvencoder.so)

```c
// Allocate an encoder context for the given codec type.
// VENC_CODEC_H264 = 0.  Returns NULL on failure.
// Note: internally calls VeInitialize — do NOT call it yourself.
VideoEncoder *VideoEncCreate(VENC_CODEC_TYPE codec);

// Configure and initialise the encoder with VencBaseConfig.
// Must be called once after VideoEncCreate, before AllocInputBuffer.
// Returns 0 on success, non-zero on failure.
int VideoEncInit(VideoEncoder *enc, VencBaseConfig *cfg);

// Tear down the encoder state but keep the context alive.
// Call before VideoEncDestroy.
void VideoEncUnInit(VideoEncoder *enc);

// Free the encoder context.  Internally calls VeRelease.
void VideoEncDestroy(VideoEncoder *enc);
```

### 3.3 Input buffer management

```c
// Allocate a pool of ION input buffers.
// nBufferNum=1 is sufficient for sequential single-threaded encode.
// nSizeY = width * height  (Y plane, one byte per pixel)
// nSizeC = width * height / 2  (UV plane, NV12 interleaved)
int AllocInputBuffer(VideoEncoder *enc, VencAllocateBufferParam *param);

// Get one free buffer from the pool.  Fills in VencInputBuffer with
// the virtual (CPU-accessible) and physical addresses of the ION buffer.
// Block until a buffer is available.
int GetOneAllocInputBuffer(VideoEncoder *enc, VencInputBuffer *buf);

// Flush the CPU cache for a buffer before submitting it to hardware.
// Required after writing YUV data into buf->_virY / buf->_virUV.
int FlushCacheAllocInputBuffer(VideoEncoder *enc, VencInputBuffer *buf);

// Submit a filled buffer for encoding.
int AddOneInputBuffer(VideoEncoder *enc, VencInputBuffer *buf);

// Release the entire ION buffer pool.  Call after the encode loop.
int ReleaseAllocInputBuffer(VideoEncoder *enc);
```

### 3.4 Encode and output

```c
// Trigger encoding of the next queued input frame.
// Asynchronous — check ValidBitstreamFrameNum before retrieving output.
int VideoEncodeOneFrame(VideoEncoder *enc);

// Returns the number of encoded frames waiting in the output queue.
// Always check this before calling GetOneBitstreamFrame.
int ValidBitstreamFrameNum(VideoEncoder *enc);

// Retrieve one encoded NAL unit.  Fills in pData0/nSize0 (and optionally
// pData1/nSize1 for wrap-around in the ring buffer).
int GetOneBitstreamFrame(VideoEncoder *enc, VencOutputBuffer *buf);

// Return the bitstream buffer to the hardware ring.
// Must be called for every successful GetOneBitstreamFrame.
int FreeOneBitStreamFrame(VideoEncoder *enc, VencOutputBuffer *buf);
```

### 3.5 Input buffer reclaim

```c
// After VideoEncodeOneFrame completes, reclaim the input buffer that was
// consumed.  The returned VencInputBuffer can be passed back to
// ReturnOneAllocInputBuffer, then GetOneAllocInputBuffer can be called
// again for the next frame.
int AlreadyUsedInputBuffer(VideoEncoder *enc, VencInputBuffer *buf);

// Return a consumed input buffer to the free pool.
int ReturnOneAllocInputBuffer(VideoEncoder *enc, VencInputBuffer *buf);
```

### 3.6 Parameter API (partially functional)

```c
// Set an encoder parameter.
// Most indices are NOT supported in this libvencoder build — the function
// returns -1 and logs "h264 do not support this N indexType".
// See Section 5 for details.
int VideoEncSetParameter(VideoEncoder *enc, int index, void *value);

// Read an encoder parameter.
// Only indices 8, 14 (resolution), and 16 (8 Mbps VBV ceiling) succeed.
int VideoEncGetParameter(VideoEncoder *enc, int index, void *value);
```

Known named parameter constants (from binary strings; numeric values unknown):

| Name | Purpose |
|---|---|
| `VENC_IndexParamH264Param` | Full H.264 config struct (supported by SetParameter) |
| `VENC_IndexParamSize` | Resolution (width × height) — index ~14 |
| `VENC_IndexParamForceKeyFrame` | Force next frame as IDR |
| `VENC_IndexParamBitrate` | Target bitrate — **not functional** in this build |
| `VENC_IndexParamFramerate` | Frame rate |
| `VENC_IndexParamMaxKeyInterval` | GOP / keyframe interval |
| `VENC_IndexParamH264FixQP` | Fixed QP mode |
| `VENC_IndexParamH264QPRange` | Min/max QP |
| `VENC_IndexParamSetVbvSize` | VBV buffer size |
| `VENC_IndexParamRotation` | Rotation |
| `VENC_IndexParamH264ProfileLevel` | H.264 profile and level |

---

## 4. Data Structures

> **Critical**: the vendor library writes **more bytes than `sizeof()`** into
> all three buffer structs.  Always heap-allocate with generous padding and
> never place these on the stack inside a function that will return through
> the CGO or JNI boundary.

### 4.1 VencBaseConfig

Configuration passed to `VideoEncInit`.

```c
typedef struct {
    unsigned char  bEncH264Nalu;    // 0 = Annex-B output (recommended)
    unsigned int   nInputWidth;     // source frame width  (pixels)
    unsigned int   nInputHeight;    // source frame height (pixels)
    unsigned int   nDstWidth;       // encode output width  (= nInputWidth)
    unsigned int   nDstHeight;      // encode output height (= nInputHeight)
    unsigned int   nStride;         // row stride in pixels (= nInputWidth)
    VENC_PIXEL_FMT eInputFormat;    // VENC_PIXEL_YUV420SP = 0  (NV12)
    void          *memops;          // from MemAdapterGetOpsS()
    void          *veOpsS;          // from GetVeOpsS(0)
    void          *pVeOpsSelf;      // NULL is safe on H618
    unsigned char  bOnlyWbFlag;
    unsigned char  bLbcLossyComEnFlag2x;
    unsigned char  bLbcLossyComEnFlag2_5x;
    unsigned char  bIsVbvNoCache;
} VencBaseConfig;
```

### 4.2 VencAllocateBufferParam

```c
typedef struct {
    unsigned int nBufferNum;   // number of ION buffers in the pool (1 is fine)
    unsigned int nSizeY;       // width * height
    unsigned int nSizeC;       // width * height / 2
} VencAllocateBufferParam;
```

### 4.3 VencInputBuffer

**Allocate as `calloc(1, 1024)`** — the library writes past the known fields.

```c
typedef struct {
    unsigned char *pAddrVirY;    // virtual address of Y plane (set by library)
    unsigned char *pAddrVirC;    // virtual address of C plane (set by library)
    unsigned char *pAddrPhyY;    // physical address of Y plane
    unsigned char *pAddrPhyC;    // physical address of C plane
    unsigned char *_phyUV;       // internal (library-managed)
    unsigned char *_virY;        // USE THIS to write luma data
    unsigned char *_virUV;       // USE THIS to write chroma data
    int            nID;
    int            _pad;
    long long      nPts;         // presentation timestamp in microseconds
    long long      nDuration;
    int            bIsFirstFrame; // 1 for the very first frame only
    int            bLastFrame;    // 1 for the final frame
    int            bEnableCorp;
    unsigned int   nShareBufFd;
    unsigned char  _tail[256];   // safety margin for library overflow writes
} VencInputBuffer;
```

### 4.4 VencOutputBuffer

**Allocate as `calloc(1, 512)`** — the library writes past the known fields
when outputting real bitstream data.

```c
typedef struct {
    int            _flags;
    int            _pad0[3];
    int            bIsKeyFrame;  // 1 if this NAL is an IDR frame
    unsigned int   nTotalSize;   // total bytes in this NAL
    int            nID;
    int            _align;
    unsigned char *pData0;       // pointer to first  data segment
    unsigned char *pData1;       // pointer to second data segment (ring wrap)
    unsigned int   nSize0;       // bytes in pData0
    unsigned int   nSize1;       // bytes in pData1 (0 if no wrap)
    long long      nPts;
    unsigned char  _tail[32];    // safety margin
} VencOutputBuffer;
```

---

## 5. Known Limitations and Quirks

### 5.1 Bitrate control is non-functional

`VideoEncSetParameter(enc, 1 /*bitrate*/, &bps)` returns -1 with the
warning "h264 do not support this 1 indexType".  All numeric indices 0–7
are unsupported in `H264SetParameterVer2`.  The encoder operates as
content-driven VBR with an ~8 Mbps hardware ceiling.  For typical game
screen content (mostly static menus, 2D sprites) this produces 10–200 kbps
naturally; complex motion scenes produce several hundred kbps to ~2 Mbps.

### 5.2 CdcIonFree errors on teardown

```
ERROR: cedarc <CdcIonFree:265>: free ion_handle err, ret -1 errno:22
```

These appear on every `VideoEncDestroy` call.  They are a vendor library
bug related to ION handle reference counting on this kernel version.  They
do not indicate data corruption or a resource leak — the process exits
cleanly after them.

### 5.3 VideoEncUnInit warning after failure paths

```
WARNING: cedarc <VideoEncUnInit:338>: the VideoEnc is not init currently
```

Appears when `VideoEncUnInit` is called on an encoder that failed
initialisation or was already torn down.  Safe to ignore if you use a
guard flag (`enc_init`) to track whether `VideoEncInit` succeeded.

### 5.4 Do not call VeInitialize directly

`VideoEncCreate` calls `VeInitialize` internally.  Calling it again
increments a reference count that then unbalances `VeRelease`, causing a
segfault during teardown.

### 5.5 pVeOpsSelf can be NULL on H618

Unlike some earlier Allwinner SoCs, `pVeOpsSelf` in `VencBaseConfig` does
not need to be set on H618.  Leave it NULL.

### 5.6 bIsFirstFrame does not force IDR

`VencInputBuffer.bIsFirstFrame = 1` marks the very first frame of a
session for hardware initialisation purposes.  It does NOT force a keyframe
on subsequent frames.  The named parameter `VENC_IndexParamForceKeyFrame`
exists in the library but its numeric enum value has not been determined.

---

## 6. NV12 Input Format

The Cedar VE only accepts `VENC_PIXEL_YUV420SP` (NV12) input.  Framebuffers
on TrimUI devices are BGRA (32bpp) or RGB565 (16bpp).  Convert before
submitting to the encoder.

**NV12 memory layout:**

```
[ Y plane  — width * height bytes  — one byte per pixel         ]
[ UV plane — width * height / 2 bytes — interleaved Cb,Cr pairs ]
```

**BT.601 limited-range conversion from BGRA32:**

```c
static inline unsigned char to_y(int r, int g, int b) {
    int v = ((66*r + 129*g + 25*b + 128) >> 8) + 16;
    return (unsigned char)(v < 16 ? 16 : v > 235 ? 235 : v);
}
static inline unsigned char to_cb(int r, int g, int b) {
    int v = ((-38*r - 74*g + 112*b + 128) >> 8) + 128;
    return (unsigned char)(v < 16 ? 16 : v > 240 ? 240 : v);
}
static inline unsigned char to_cr(int r, int g, int b) {
    int v = ((112*r - 94*g - 18*b + 128) >> 8) + 128;
    return (unsigned char)(v < 16 ? 16 : v > 240 ? 240 : v);
}

void bgra_to_nv12(const uint8_t *bgra,
                  uint8_t *y_out, uint8_t *uv_out,
                  unsigned int w, unsigned int h)
{
    unsigned int uv_i = 0;
    for (unsigned int row = 0; row < h; row++) {
        for (unsigned int col = 0; col < w; col++) {
            const uint8_t *p = bgra + (row * w + col) * 4;
            int b = p[0], g = p[1], r = p[2];
            y_out[row * w + col] = to_y(r, g, b);
            if ((row & 1) == 0 && (col & 1) == 0) {
                uv_out[uv_i++] = to_cb(r, g, b);
                uv_out[uv_i++] = to_cr(r, g, b);
            }
        }
    }
}
```

---

## 7. Encode Loop — Step by Step

```
dlopen libVE.so, libMemAdapter.so, libvencoder.so
          │
          ▼
GetVeOpsS(0) → veops
MemAdapterGetOpsS() → memops
memops->open()
          │
          ▼
VideoEncCreate(VENC_CODEC_H264) → enc
          │
          ▼
VideoEncInit(enc, &bcfg)          ← bcfg has width/height/format/memops/veops
          │
          ▼
AllocInputBuffer(enc, &bp)        ← bp has nBufferNum=1, nSizeY, nSizeC
          │
          ▼
GetOneAllocInputBuffer(enc, inbuf) ← inbuf->_virY / _virUV now point to ION
          │
          ▼
┌─────────────── encode loop ──────────────────────────────────┐
│  write NV12 data into inbuf->_virY and inbuf->_virUV         │
│  inbuf->nPts = frame_index * (1_000_000 / fps)               │
│  FlushCacheAllocInputBuffer(enc, inbuf)                       │
│  AddOneInputBuffer(enc, inbuf)                                │
│  VideoEncodeOneFrame(enc)                                     │
│                                                               │
│  while (ValidBitstreamFrameNum(enc) > 0):                     │
│      memset(outbuf, 0, 512)                                   │
│      GetOneBitstreamFrame(enc, outbuf)                        │
│      consume outbuf->pData0[0..nSize0-1]                      │
│      consume outbuf->pData1[0..nSize1-1]  (if nSize1 > 0)    │
│      FreeOneBitStreamFrame(enc, outbuf)                       │
│                                                               │
│  AlreadyUsedInputBuffer(enc, reclaimed)                       │
│  ReturnOneAllocInputBuffer(enc, reclaimed)                    │
│  GetOneAllocInputBuffer(enc, inbuf)       ← next frame        │
└──────────────────────────────────────────────────────────────┘
          │
          ▼
ReleaseAllocInputBuffer(enc)
VideoEncUnInit(enc)
VideoEncDestroy(enc)
memops->close()
dlclose all three libs
```

---

## 8. Code Examples

### 8.1 C — minimal single-frame encoder

```c
/*
 * cedar_encode_frame.c
 *
 * Reads one frame from /dev/fb0, encodes it as H.264, writes the NAL
 * unit to stdout.
 *
 * Build (on device or cross-compile for aarch64):
 *   aarch64-linux-gnu-gcc -O2 -o cedar_encode_frame cedar_encode_frame.c -ldl
 *
 * Run:
 *   ./cedar_encode_frame > frame.h264
 */
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <fcntl.h>
#include <unistd.h>
#include <dlfcn.h>

/* ── Minimal type definitions ─────────────────────────────────────────── */

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
    unsigned char  bOnlyWbFlag, bLbcLossyComEnFlag2x;
    unsigned char  bLbcLossyComEnFlag2_5x, bIsVbvNoCache;
} VencBaseConfig;

typedef struct {
    unsigned char *pAddrVirY, *pAddrVirC, *pAddrPhyY, *pAddrPhyC;
    unsigned char *_phyUV, *_virY, *_virUV;
    int            nID, _pad;
    long long      nPts, nDuration;
    int            bIsFirstFrame, bLastFrame, bEnableCorp;
    unsigned int   nShareBufFd;
    unsigned char  _tail[256]; /* vendor overflow safety margin */
} VencInputBuffer;

typedef struct {
    int            _flags, _pad0[3], bIsKeyFrame;
    unsigned int   nTotalSize;
    int            nID, _align;
    unsigned char *pData0, *pData1;
    unsigned int   nSize0, nSize1;
    long long      nPts;
    unsigned char  _tail[32];  /* vendor overflow safety margin */
} VencOutputBuffer;

typedef struct { unsigned int nBufferNum, nSizeY, nSizeC; } VencAllocateBufferParam;

/* ── NV12 conversion ──────────────────────────────────────────────────── */

static void bgra_to_nv12(const uint8_t *bgra, uint8_t *y, uint8_t *uv,
                          unsigned int w, unsigned int h)
{
    unsigned int uv_i = 0;
    for (unsigned int row = 0; row < h; row++) {
        for (unsigned int col = 0; col < w; col++) {
            const uint8_t *p = bgra + (row * w + col) * 4;
            int b = p[0], g = p[1], r = p[2];
            int yv = ((66*r + 129*g + 25*b + 128) >> 8) + 16;
            y[row * w + col] = (uint8_t)(yv < 16 ? 16 : yv > 235 ? 235 : yv);
            if ((row & 1) == 0 && (col & 1) == 0) {
                int cb = ((-38*r - 74*g + 112*b + 128) >> 8) + 128;
                int cr = ((112*r - 94*g - 18*b + 128) >> 8) + 128;
                uv[uv_i++] = (uint8_t)(cb < 16 ? 16 : cb > 240 ? 240 : cb);
                uv[uv_i++] = (uint8_t)(cr < 16 ? 16 : cr > 240 ? 240 : cr);
            }
        }
    }
}

/* ── main ─────────────────────────────────────────────────────────────── */

int main(void)
{
    const unsigned int W = 1280, H = 720;

    /* Load libraries */
    void *libVE   = dlopen("libVE.so",         RTLD_LAZY | RTLD_GLOBAL);
    void *libMem  = dlopen("libMemAdapter.so", RTLD_LAZY | RTLD_GLOBAL);
    void *libVenc = dlopen("libvencoder.so",   RTLD_LAZY | RTLD_GLOBAL);
    if (!libVE || !libMem || !libVenc) { fprintf(stderr, "%s\n", dlerror()); return 1; }

    /* Resolve symbols */
    void       *(*GetVeOpsS)(int)        = dlsym(libVE,   "GetVeOpsS");
    void       *(*MemAdapterGetOpsS)(void) = dlsym(libMem, "MemAdapterGetOpsS");
    VideoEncoder *(*VideoEncCreate)(VENC_CODEC_TYPE)  = dlsym(libVenc, "VideoEncCreate");
    int  (*VideoEncInit)(VideoEncoder *, VencBaseConfig *)  = dlsym(libVenc, "VideoEncInit");
    void (*VideoEncUnInit)(VideoEncoder *)               = dlsym(libVenc, "VideoEncUnInit");
    void (*VideoEncDestroy)(VideoEncoder *)              = dlsym(libVenc, "VideoEncDestroy");
    int  (*AllocInputBuffer)(VideoEncoder *, VencAllocateBufferParam *) = dlsym(libVenc, "AllocInputBuffer");
    int  (*GetOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *)   = dlsym(libVenc, "GetOneAllocInputBuffer");
    int  (*FlushCacheAllocInputBuffer)(VideoEncoder *, VencInputBuffer *) = dlsym(libVenc, "FlushCacheAllocInputBuffer");
    int  (*AddOneInputBuffer)(VideoEncoder *, VencInputBuffer *)        = dlsym(libVenc, "AddOneInputBuffer");
    int  (*VideoEncodeOneFrame)(VideoEncoder *)                         = dlsym(libVenc, "VideoEncodeOneFrame");
    int  (*ValidBitstreamFrameNum)(VideoEncoder *)                      = dlsym(libVenc, "ValidBitstreamFrameNum");
    int  (*GetOneBitstreamFrame)(VideoEncoder *, VencOutputBuffer *)    = dlsym(libVenc, "GetOneBitstreamFrame");
    int  (*FreeOneBitStreamFrame)(VideoEncoder *, VencOutputBuffer *)   = dlsym(libVenc, "FreeOneBitStreamFrame");
    int  (*AlreadyUsedInputBuffer)(VideoEncoder *, VencInputBuffer *)   = dlsym(libVenc, "AlreadyUsedInputBuffer");
    int  (*ReturnOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *) = dlsym(libVenc, "ReturnOneAllocInputBuffer");
    int  (*ReleaseAllocInputBuffer)(VideoEncoder *)                     = dlsym(libVenc, "ReleaseAllocInputBuffer");

    /* Initialise memory subsystem */
    void      *veops  = GetVeOpsS(0);
    ScMemOpsS *memops = MemAdapterGetOpsS();
    memops->open();

    /* Create and configure encoder */
    VideoEncoder *enc = VideoEncCreate(VENC_CODEC_H264);
    VencBaseConfig bcfg = {0};
    bcfg.nInputWidth = bcfg.nDstWidth = bcfg.nStride = W;
    bcfg.nInputHeight = bcfg.nDstHeight = H;
    bcfg.eInputFormat = VENC_PIXEL_YUV420SP;
    bcfg.memops = memops;
    bcfg.veOpsS = veops;
    VideoEncInit(enc, &bcfg);

    /* Allocate ION input buffer */
    VencAllocateBufferParam bp = { .nBufferNum = 1, .nSizeY = W*H, .nSizeC = W*H/2 };
    AllocInputBuffer(enc, &bp);

    /* Heap-allocate structs — vendor writes past sizeof() */
    VencInputBuffer  *inbuf  = calloc(1, 1024);
    VencInputBuffer  *reclaimed = calloc(1, 1024);
    VencOutputBuffer *outbuf = calloc(1, 512);
    GetOneAllocInputBuffer(enc, inbuf);

    /* Read one frame from the framebuffer */
    uint8_t *fbdata = malloc(W * H * 4);
    int fb = open("/dev/fb0", O_RDONLY);
    pread(fb, fbdata, W * H * 4, 0);
    close(fb);

    /* Convert BGRA → NV12 and encode */
    bgra_to_nv12(fbdata, inbuf->_virY, inbuf->_virUV, W, H);
    inbuf->bIsFirstFrame = 1;
    inbuf->nPts = 0;
    FlushCacheAllocInputBuffer(enc, inbuf);
    AddOneInputBuffer(enc, inbuf);
    VideoEncodeOneFrame(enc);

    /* Drain output */
    while (ValidBitstreamFrameNum(enc) > 0) {
        memset(outbuf, 0, 512);
        if (GetOneBitstreamFrame(enc, outbuf) != 0) break;
        if (outbuf->pData0 && outbuf->nSize0 > 0)
            fwrite(outbuf->pData0, 1, outbuf->nSize0, stdout);
        if (outbuf->pData1 && outbuf->nSize1 > 0)
            fwrite(outbuf->pData1, 1, outbuf->nSize1, stdout);
        FreeOneBitStreamFrame(enc, outbuf);
    }

    /* Reclaim input buffer */
    AlreadyUsedInputBuffer(enc, reclaimed);
    ReturnOneAllocInputBuffer(enc, reclaimed);

    /* Teardown */
    ReleaseAllocInputBuffer(enc);
    VideoEncUnInit(enc);
    VideoEncDestroy(enc);
    memops->close();
    free(outbuf); free(reclaimed); free(inbuf); free(fbdata);
    dlclose(libVenc); dlclose(libMem); dlclose(libVE);
    return 0;
}
```

---

### 8.2 C++ — RAII encoder wrapper

```cpp
/*
 * CedarEncoder.hpp / CedarEncoder.cpp
 *
 * C++ RAII wrapper around the Cedar H.264 encoder.
 *
 * Build:
 *   aarch64-linux-gnu-g++ -O2 -std=c++17 -o cedar_demo \
 *       cedar_demo.cpp -ldl
 */
#pragma once
#include <cstdint>
#include <cstring>
#include <cstdlib>
#include <functional>
#include <stdexcept>
#include <string>
#include <dlfcn.h>
#include <fcntl.h>
#include <unistd.h>

// ── Forward declarations matching vendor ABI ──────────────────────────

extern "C" {

typedef enum { VENC_CODEC_H264 = 0 } VENC_CODEC_TYPE;
typedef enum { VENC_PIXEL_YUV420SP = 0 } VENC_PIXEL_FMT;
struct VideoEncoder;

struct ScMemOpsS {
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
};

struct VencBaseConfig {
    uint8_t        bEncH264Nalu;
    unsigned int   nInputWidth, nInputHeight, nDstWidth, nDstHeight, nStride;
    VENC_PIXEL_FMT eInputFormat;
    void          *memops, *veOpsS, *pVeOpsSelf;
    uint8_t        bOnlyWbFlag, bLbcLossyComEnFlag2x;
    uint8_t        bLbcLossyComEnFlag2_5x, bIsVbvNoCache;
};

struct VencAllocateBufferParam { unsigned int nBufferNum, nSizeY, nSizeC; };

struct VencInputBuffer {
    uint8_t       *pAddrVirY, *pAddrVirC, *pAddrPhyY, *pAddrPhyC;
    uint8_t       *_phyUV, *_virY, *_virUV;
    int            nID, _pad;
    long long      nPts, nDuration;
    int            bIsFirstFrame, bLastFrame, bEnableCorp;
    unsigned int   nShareBufFd;
    uint8_t        _tail[256];
};

struct VencOutputBuffer {
    int            _flags, _pad0[3], bIsKeyFrame;
    unsigned int   nTotalSize;
    int            nID, _align;
    uint8_t       *pData0, *pData1;
    unsigned int   nSize0, nSize1;
    long long      nPts;
    uint8_t        _tail[32];
};

} // extern "C"

// ── CedarEncoder class ────────────────────────────────────────────────

class CedarEncoder {
public:
    using FrameCallback = std::function<void(const uint8_t *, size_t, bool /*is_key*/)>;

    struct Config {
        unsigned int width  = 1280;
        unsigned int height = 720;
        unsigned int fps    = 30;
    };

    explicit CedarEncoder(const Config &cfg) : cfg_(cfg) { open(); }
    ~CedarEncoder() { close(); }

    CedarEncoder(const CedarEncoder &) = delete;
    CedarEncoder &operator=(const CedarEncoder &) = delete;

    // Encode one NV12 frame.  Calls cb for each output NAL unit.
    void encode(const uint8_t *nv12_y, const uint8_t *nv12_uv,
                long long pts_us, bool first_frame,
                FrameCallback cb)
    {
        std::memcpy(inbuf_->_virY,  nv12_y,  cfg_.width * cfg_.height);
        std::memcpy(inbuf_->_virUV, nv12_uv, cfg_.width * cfg_.height / 2);
        inbuf_->nPts          = pts_us;
        inbuf_->bIsFirstFrame = first_frame ? 1 : 0;

        fn_.FlushCacheAllocInputBuffer(enc_, inbuf_);
        fn_.AddOneInputBuffer(enc_, inbuf_);
        fn_.VideoEncodeOneFrame(enc_);

        while (fn_.ValidBitstreamFrameNum(enc_) > 0) {
            std::memset(outbuf_, 0, 512);
            if (fn_.GetOneBitstreamFrame(enc_, outbuf_) != 0) break;
            bool key = outbuf_->bIsKeyFrame != 0;
            if (outbuf_->pData0 && outbuf_->nSize0 > 0)
                cb(outbuf_->pData0, outbuf_->nSize0, key);
            if (outbuf_->pData1 && outbuf_->nSize1 > 0)
                cb(outbuf_->pData1, outbuf_->nSize1, key);
            fn_.FreeOneBitStreamFrame(enc_, outbuf_);
        }

        std::memset(reclaimed_, 0, 1024);
        fn_.AlreadyUsedInputBuffer(enc_, reclaimed_);
        fn_.ReturnOneAllocInputBuffer(enc_, reclaimed_);
        std::memset(inbuf_, 0, 1024);
        fn_.GetOneAllocInputBuffer(enc_, inbuf_);
    }

private:
    struct Fns {
        void       *(*GetVeOpsS)(int);
        void       *(*MemAdapterGetOpsS)(void);
        VideoEncoder *(*VideoEncCreate)(VENC_CODEC_TYPE);
        int  (*VideoEncInit)(VideoEncoder *, VencBaseConfig *);
        void (*VideoEncUnInit)(VideoEncoder *);
        void (*VideoEncDestroy)(VideoEncoder *);
        int  (*AllocInputBuffer)(VideoEncoder *, VencAllocateBufferParam *);
        int  (*GetOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
        int  (*FlushCacheAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
        int  (*ReturnOneAllocInputBuffer)(VideoEncoder *, VencInputBuffer *);
        int  (*ReleaseAllocInputBuffer)(VideoEncoder *);
        int  (*AddOneInputBuffer)(VideoEncoder *, VencInputBuffer *);
        int  (*VideoEncodeOneFrame)(VideoEncoder *);
        int  (*ValidBitstreamFrameNum)(VideoEncoder *);
        int  (*GetOneBitstreamFrame)(VideoEncoder *, VencOutputBuffer *);
        int  (*FreeOneBitStreamFrame)(VideoEncoder *, VencOutputBuffer *);
        int  (*AlreadyUsedInputBuffer)(VideoEncoder *, VencInputBuffer *);
    } fn_{};

    Config        cfg_;
    void         *libVE_ = nullptr, *libMem_ = nullptr, *libVenc_ = nullptr;
    ScMemOpsS    *memops_ = nullptr;
    VideoEncoder *enc_    = nullptr;
    VencInputBuffer  *inbuf_     = nullptr;
    VencInputBuffer  *reclaimed_ = nullptr;
    VencOutputBuffer *outbuf_    = nullptr;
    bool enc_init_ = false, buf_alloc_ = false;

    template<class T>
    T sym(void *lib, const char *name) {
        T f; *reinterpret_cast<void **>(&f) = dlsym(lib, name);
        if (!f) throw std::runtime_error(std::string("dlsym(") + name + "): " + dlerror());
        return f;
    }

    void open() {
        libVE_   = dlopen("libVE.so",         RTLD_LAZY | RTLD_GLOBAL);
        libMem_  = dlopen("libMemAdapter.so", RTLD_LAZY | RTLD_GLOBAL);
        libVenc_ = dlopen("libvencoder.so",   RTLD_LAZY | RTLD_GLOBAL);
        if (!libVE_ || !libMem_ || !libVenc_)
            throw std::runtime_error(dlerror());

        fn_.GetVeOpsS               = sym<decltype(fn_.GetVeOpsS)>              (libVE_,   "GetVeOpsS");
        fn_.MemAdapterGetOpsS       = sym<decltype(fn_.MemAdapterGetOpsS)>      (libMem_,  "MemAdapterGetOpsS");
        fn_.VideoEncCreate          = sym<decltype(fn_.VideoEncCreate)>         (libVenc_, "VideoEncCreate");
        fn_.VideoEncInit            = sym<decltype(fn_.VideoEncInit)>           (libVenc_, "VideoEncInit");
        fn_.VideoEncUnInit          = sym<decltype(fn_.VideoEncUnInit)>         (libVenc_, "VideoEncUnInit");
        fn_.VideoEncDestroy         = sym<decltype(fn_.VideoEncDestroy)>        (libVenc_, "VideoEncDestroy");
        fn_.AllocInputBuffer        = sym<decltype(fn_.AllocInputBuffer)>       (libVenc_, "AllocInputBuffer");
        fn_.GetOneAllocInputBuffer  = sym<decltype(fn_.GetOneAllocInputBuffer)> (libVenc_, "GetOneAllocInputBuffer");
        fn_.FlushCacheAllocInputBuffer = sym<decltype(fn_.FlushCacheAllocInputBuffer)>(libVenc_, "FlushCacheAllocInputBuffer");
        fn_.ReturnOneAllocInputBuffer  = sym<decltype(fn_.ReturnOneAllocInputBuffer)> (libVenc_, "ReturnOneAllocInputBuffer");
        fn_.ReleaseAllocInputBuffer    = sym<decltype(fn_.ReleaseAllocInputBuffer)>   (libVenc_, "ReleaseAllocInputBuffer");
        fn_.AddOneInputBuffer       = sym<decltype(fn_.AddOneInputBuffer)>      (libVenc_, "AddOneInputBuffer");
        fn_.VideoEncodeOneFrame     = sym<decltype(fn_.VideoEncodeOneFrame)>    (libVenc_, "VideoEncodeOneFrame");
        fn_.ValidBitstreamFrameNum  = sym<decltype(fn_.ValidBitstreamFrameNum)> (libVenc_, "ValidBitstreamFrameNum");
        fn_.GetOneBitstreamFrame    = sym<decltype(fn_.GetOneBitstreamFrame)>   (libVenc_, "GetOneBitstreamFrame");
        fn_.FreeOneBitStreamFrame   = sym<decltype(fn_.FreeOneBitStreamFrame)>  (libVenc_, "FreeOneBitStreamFrame");
        fn_.AlreadyUsedInputBuffer  = sym<decltype(fn_.AlreadyUsedInputBuffer)> (libVenc_, "AlreadyUsedInputBuffer");

        void *veops = fn_.GetVeOpsS(0);
        memops_ = reinterpret_cast<ScMemOpsS *>(fn_.MemAdapterGetOpsS());
        if (!veops || !memops_) throw std::runtime_error("ops init failed");
        memops_->open();

        enc_ = fn_.VideoEncCreate(VENC_CODEC_H264);
        if (!enc_) throw std::runtime_error("VideoEncCreate failed");

        VencBaseConfig bcfg{};
        bcfg.nInputWidth = bcfg.nDstWidth = bcfg.nStride = cfg_.width;
        bcfg.nInputHeight = bcfg.nDstHeight = cfg_.height;
        bcfg.eInputFormat = VENC_PIXEL_YUV420SP;
        bcfg.memops = memops_;
        bcfg.veOpsS = veops;
        if (fn_.VideoEncInit(enc_, &bcfg) != 0)
            throw std::runtime_error("VideoEncInit failed");
        enc_init_ = true;

        VencAllocateBufferParam bp{ 1, cfg_.width * cfg_.height, cfg_.width * cfg_.height / 2 };
        if (fn_.AllocInputBuffer(enc_, &bp) != 0)
            throw std::runtime_error("AllocInputBuffer failed");
        buf_alloc_ = true;

        inbuf_     = reinterpret_cast<VencInputBuffer *>(calloc(1, 1024));
        reclaimed_ = reinterpret_cast<VencInputBuffer *>(calloc(1, 1024));
        outbuf_    = reinterpret_cast<VencOutputBuffer *>(calloc(1, 512));
        if (!inbuf_ || !reclaimed_ || !outbuf_) throw std::bad_alloc();

        if (fn_.GetOneAllocInputBuffer(enc_, inbuf_) != 0)
            throw std::runtime_error("GetOneAllocInputBuffer failed");
    }

    void close() noexcept {
        if (buf_alloc_) fn_.ReleaseAllocInputBuffer(enc_);
        if (enc_init_)  fn_.VideoEncUnInit(enc_);
        if (enc_)       fn_.VideoEncDestroy(enc_);
        if (memops_)    memops_->close();
        std::free(outbuf_); std::free(reclaimed_); std::free(inbuf_);
        if (libVenc_) dlclose(libVenc_);
        if (libMem_)  dlclose(libMem_);
        if (libVE_)   dlclose(libVE_);
    }
};

// ── Usage example ─────────────────────────────────────────────────────
//
//  int main() {
//      CedarEncoder enc({ .width=1280, .height=720, .fps=30 });
//      // nv12_y and nv12_uv must point to ION-filled NV12 planes
//      enc.encode(nv12_y, nv12_uv, 0, true,
//                 [](const uint8_t *data, size_t len, bool key) {
//                     fwrite(data, 1, len, stdout);
//                 });
//  }
```

---

### 8.3 Go — CGO binding

```go
// cedar_encoder.go
//
// Wraps cedar_encoder_linux_arm64.c via CGO.
// Build constraints ensure this compiles only on linux/arm64.
//
// Usage:
//
//   enc, err := NewCedarEncoder(1280, 720, 30)
//   if err != nil { log.Fatal(err) }
//   defer enc.Close()
//
//   for frame := range frames {
//       enc.Encode(frame.NV12Y, frame.NV12UV, frame.PTS, func(nal []byte, key bool) {
//           mux.WriteNAL(nal, key)
//       })
//   }

//go:build linux && arm64

package stream

/*
#cgo LDFLAGS: -ldl

#include "cedar_encoder_linux_arm64.c"
*/
import "C"
import (
    "fmt"
    "runtime/cgo"
    "unsafe"
)

// NalCallback is called with each output NAL unit from the encoder.
type NalCallback func(nal []byte, isKeyFrame bool)

// CedarEncoder wraps the C cedar_run encode loop.
type CedarEncoder struct {
    cfg    C.cedar_cfg_t
    stop   C.int
    handle cgo.Handle
}

// NewCedarEncoder creates and starts a Cedar hardware H.264 encoder.
// width and height must match the framebuffer resolution (1280×720 on tg5040).
// fps controls the PTS increment per frame; the hardware does not enforce it.
func NewCedarEncoder(width, height, fps uint) (*CedarEncoder, error) {
    e := &CedarEncoder{}
    e.cfg.width  = C.uint(width)
    e.cfg.height = C.uint(height)
    e.cfg.fps    = C.uint(fps)
    e.cfg.bpp    = 32 // BGRA framebuffer on tg5040
    return e, nil
}

// EncodeFramebuffer encodes the current contents of /dev/fb0.
// cb is called synchronously for each NAL unit produced.
func (e *CedarEncoder) EncodeFramebuffer(pts int64, cb NalCallback) error {
    e.handle = cgo.NewHandle(cb)
    defer e.handle.Delete()

    e.cfg.writer_handle = C.uintptr_t(e.handle)
    e.stop = 0

    ret := C.cedar_run(&e.cfg, &e.stop)
    if ret != 0 {
        return fmt.Errorf("cedar_run returned %d", ret)
    }
    return nil
}

// Stop signals the encode loop to exit after the current frame.
func (e *CedarEncoder) Stop() { e.stop = 1 }

// Close shuts down the encoder and releases all resources.
func (e *CedarEncoder) Close() { e.Stop() }

//export cedar_write_go
func cedar_write_go(handle C.uintptr_t, data unsafe.Pointer, n C.int) C.int {
    cb := cgo.Handle(handle).Value().(NalCallback)
    buf := unsafe.Slice((*byte)(data), int(n))
    cb(buf, false) // key frame detection: parse NAL type from buf[4]&0x1f == 5
    return 0
}
```

---

### 8.4 Rust — libloading binding

```rust
// cedar_encoder.rs
//
// Rust binding to the Cedar H.264 encoder using libloading for dlopen.
//
// Add to Cargo.toml:
//   [dependencies]
//   libloading = "0.8"
//
// Build for target:
//   cargo build --target aarch64-unknown-linux-gnu --release

use libloading::{Library, Symbol};
use std::ffi::c_void;

// ── ABI types ─────────────────────────────────────────────────────────

#[repr(C)]
struct ScMemOpsS {
    open:            extern "C" fn() -> i32,
    open2:           extern "C" fn(*mut c_void, *mut c_void) -> i32,
    close:           extern "C" fn(),
    total_size:      extern "C" fn() -> i32,
    palloc:          extern "C" fn(i32, *mut c_void, *mut c_void) -> *mut c_void,
    palloc_no_cache: extern "C" fn(i32, *mut c_void, *mut c_void) -> *mut c_void,
    pfree:           extern "C" fn(*mut c_void, *mut c_void, *mut c_void),
    flush_cache:     extern "C" fn(*mut c_void, i32),
    ve_get_phyaddr:  extern "C" fn(*mut c_void) -> *mut c_void,
    ve_get_viraddr:  extern "C" fn(*mut c_void) -> *mut c_void,
    cpu_get_phyaddr: extern "C" fn(*mut c_void) -> *mut c_void,
    cpu_get_viraddr: extern "C" fn(*mut c_void) -> *mut c_void,
    mem_set:         extern "C" fn(*mut c_void, i32, usize) -> i32,
    mem_cpy:         extern "C" fn(*mut c_void, *mut c_void, usize) -> i32,
    mem_read:        extern "C" fn(*mut c_void, *mut c_void, usize) -> i32,
    mem_write:       extern "C" fn(*mut c_void, *mut c_void, usize) -> i32,
    setup:           extern "C" fn() -> i32,
    shutdown:        extern "C" fn() -> i32,
    get_ve_addr_offset: extern "C" fn() -> u32,
    get_debug_info:  extern "C" fn(*mut u8, i32) -> i32,
    get_vir_by_fd:   extern "C" fn(i32) -> *mut c_void,
    get_phy_by_fd:   extern "C" fn(i32, *mut c_void) -> i32,
    free_phy_by_fd:  extern "C" fn(i32, u64) -> i32,
    get_fd_by_vir:   extern "C" fn(*mut c_void) -> i32,
}

#[repr(C)]
struct VencBaseConfig {
    b_enc_h264_nalu:  u8,
    n_input_width:    u32,
    n_input_height:   u32,
    n_dst_width:      u32,
    n_dst_height:     u32,
    n_stride:         u32,
    e_input_format:   u32, // VENC_PIXEL_YUV420SP = 0
    memops:           *mut c_void,
    ve_ops_s:         *mut c_void,
    p_ve_ops_self:    *mut c_void,
    b_only_wb_flag:   u8,
    b_lbc_lossy_2x:   u8,
    b_lbc_lossy_2_5x: u8,
    b_is_vbv_no_cache: u8,
}

#[repr(C)]
struct VencAllocateBufferParam {
    n_buffer_num: u32,
    n_size_y:     u32,
    n_size_c:     u32,
}

// Both input/output buffers are over-allocated on the heap to absorb
// vendor writes past the known struct size.
const INPUT_BUF_SIZE:  usize = 1024;
const OUTPUT_BUF_SIZE: usize = 512;

// Byte offsets of the fields we actually use within VencInputBuffer.
// Determined empirically — no header available.
const INBUF_VIR_Y_OFFSET:  usize = 40; // *mut u8 (_virY)
const INBUF_VIR_UV_OFFSET: usize = 48; // *mut u8 (_virUV)
const INBUF_PTS_OFFSET:    usize = 64; // i64 (nPts)
const INBUF_FIRST_OFFSET:  usize = 72; // i32 (bIsFirstFrame)

// Byte offsets within VencOutputBuffer.
const OUTBUF_KEY_OFFSET:   usize = 16; // i32 (bIsKeyFrame)
const OUTBUF_PDATA0_OFFSET: usize = 32; // *mut u8 (pData0)
const OUTBUF_PDATA1_OFFSET: usize = 40; // *mut u8 (pData1)
const OUTBUF_SIZE0_OFFSET: usize = 48; // u32 (nSize0)
const OUTBUF_SIZE1_OFFSET: usize = 52; // u32 (nSize1)

unsafe fn read_ptr(buf: &[u8], offset: usize) -> *mut u8 {
    let val: usize = std::ptr::read_unaligned(
        buf.as_ptr().add(offset) as *const usize
    );
    val as *mut u8
}

unsafe fn read_u32(buf: &[u8], offset: usize) -> u32 {
    std::ptr::read_unaligned(buf.as_ptr().add(offset) as *const u32)
}

// ── CedarEncoder ──────────────────────────────────────────────────────

pub struct CedarEncoder {
    _lib_ve:   Library,
    _lib_mem:  Library,
    lib_venc:  Library,
    memops:    *mut ScMemOpsS,
    enc:       *mut c_void,
    inbuf:     Box<[u8; INPUT_BUF_SIZE]>,
    reclaimed: Box<[u8; INPUT_BUF_SIZE]>,
    outbuf:    Box<[u8; OUTPUT_BUF_SIZE]>,
    enc_init:  bool,
    buf_alloc: bool,
    width:     u32,
    height:    u32,
}

impl CedarEncoder {
    pub fn new(width: u32, height: u32) -> Result<Self, Box<dyn std::error::Error>> {
        unsafe {
            let lib_ve  = Library::new("libVE.so")?;
            let lib_mem = Library::new("libMemAdapter.so")?;
            let lib_venc = Library::new("libvencoder.so")?;

            let get_ve_ops_s: Symbol<extern "C" fn(i32) -> *mut c_void> =
                lib_ve.get(b"GetVeOpsS\0")?;
            let mem_adapter_get_ops_s: Symbol<extern "C" fn() -> *mut ScMemOpsS> =
                lib_mem.get(b"MemAdapterGetOpsS\0")?;
            let video_enc_create: Symbol<extern "C" fn(u32) -> *mut c_void> =
                lib_venc.get(b"VideoEncCreate\0")?;
            let video_enc_init: Symbol<extern "C" fn(*mut c_void, *mut VencBaseConfig) -> i32> =
                lib_venc.get(b"VideoEncInit\0")?;
            let alloc_input_buffer: Symbol<extern "C" fn(*mut c_void, *mut VencAllocateBufferParam) -> i32> =
                lib_venc.get(b"AllocInputBuffer\0")?;
            let get_one_alloc_input_buffer: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                lib_venc.get(b"GetOneAllocInputBuffer\0")?;

            let veops  = get_ve_ops_s(0);
            let memops = mem_adapter_get_ops_s();
            if veops.is_null() || memops.is_null() {
                return Err("ops init failed".into());
            }
            ((*memops).open)();

            let enc = video_enc_create(0 /* VENC_CODEC_H264 */);
            if enc.is_null() { return Err("VideoEncCreate failed".into()); }

            let mut bcfg = VencBaseConfig {
                b_enc_h264_nalu: 0,
                n_input_width: width, n_input_height: height,
                n_dst_width: width,   n_dst_height: height,
                n_stride: width,
                e_input_format: 0, // NV12
                memops: memops as *mut c_void,
                ve_ops_s: veops,
                p_ve_ops_self: std::ptr::null_mut(),
                b_only_wb_flag: 0, b_lbc_lossy_2x: 0,
                b_lbc_lossy_2_5x: 0, b_is_vbv_no_cache: 0,
            };
            if video_enc_init(enc, &mut bcfg) != 0 {
                return Err("VideoEncInit failed".into());
            }

            let mut bp = VencAllocateBufferParam {
                n_buffer_num: 1,
                n_size_y: width * height,
                n_size_c: width * height / 2,
            };
            if alloc_input_buffer(enc, &mut bp) != 0 {
                return Err("AllocInputBuffer failed".into());
            }

            let mut inbuf: Box<[u8; INPUT_BUF_SIZE]> = Box::new([0u8; INPUT_BUF_SIZE]);
            if get_one_alloc_input_buffer(enc, inbuf.as_mut_ptr()) != 0 {
                return Err("GetOneAllocInputBuffer failed".into());
            }

            Ok(CedarEncoder {
                _lib_ve: lib_ve, _lib_mem: lib_mem, lib_venc,
                memops, enc,
                inbuf,
                reclaimed: Box::new([0u8; INPUT_BUF_SIZE]),
                outbuf:    Box::new([0u8; OUTPUT_BUF_SIZE]),
                enc_init:  true,
                buf_alloc: true,
                width, height,
            })
        }
    }

    /// Encode one NV12 frame.  `cb` receives each output NAL unit.
    pub fn encode<F>(&mut self, nv12_y: &[u8], nv12_uv: &[u8],
                     pts_us: i64, first_frame: bool,
                     mut cb: F) -> Result<(), Box<dyn std::error::Error>>
    where F: FnMut(&[u8], bool)
    {
        unsafe {
            let vir_y  = read_ptr(&self.inbuf, INBUF_VIR_Y_OFFSET);
            let vir_uv = read_ptr(&self.inbuf, INBUF_VIR_UV_OFFSET);
            if vir_y.is_null() || vir_uv.is_null() {
                return Err("NULL virtual addresses".into());
            }

            let y_size = (self.width * self.height) as usize;
            std::ptr::copy_nonoverlapping(nv12_y.as_ptr(),  vir_y,  y_size);
            std::ptr::copy_nonoverlapping(nv12_uv.as_ptr(), vir_uv, y_size / 2);

            std::ptr::write_unaligned(
                self.inbuf.as_mut_ptr().add(INBUF_PTS_OFFSET) as *mut i64, pts_us);
            std::ptr::write_unaligned(
                self.inbuf.as_mut_ptr().add(INBUF_FIRST_OFFSET) as *mut i32,
                if first_frame { 1 } else { 0 });

            let flush: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                self.lib_venc.get(b"FlushCacheAllocInputBuffer\0")?;
            let add: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                self.lib_venc.get(b"AddOneInputBuffer\0")?;
            let encode_one: Symbol<extern "C" fn(*mut c_void) -> i32> =
                self.lib_venc.get(b"VideoEncodeOneFrame\0")?;
            let valid_num: Symbol<extern "C" fn(*mut c_void) -> i32> =
                self.lib_venc.get(b"ValidBitstreamFrameNum\0")?;
            let get_one_out: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                self.lib_venc.get(b"GetOneBitstreamFrame\0")?;
            let free_out: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                self.lib_venc.get(b"FreeOneBitStreamFrame\0")?;
            let already_used: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                self.lib_venc.get(b"AlreadyUsedInputBuffer\0")?;
            let return_one: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                self.lib_venc.get(b"ReturnOneAllocInputBuffer\0")?;
            let get_one_in: Symbol<extern "C" fn(*mut c_void, *mut u8) -> i32> =
                self.lib_venc.get(b"GetOneAllocInputBuffer\0")?;

            flush(self.enc, self.inbuf.as_mut_ptr());
            add(self.enc, self.inbuf.as_mut_ptr());
            encode_one(self.enc);

            while valid_num(self.enc) > 0 {
                self.outbuf.fill(0);
                if get_one_out(self.enc, self.outbuf.as_mut_ptr()) != 0 { break; }

                let key = read_u32(&self.outbuf, OUTBUF_KEY_OFFSET) != 0;
                let p0  = read_ptr(&self.outbuf, OUTBUF_PDATA0_OFFSET);
                let s0  = read_u32(&self.outbuf, OUTBUF_SIZE0_OFFSET) as usize;
                let p1  = read_ptr(&self.outbuf, OUTBUF_PDATA1_OFFSET);
                let s1  = read_u32(&self.outbuf, OUTBUF_SIZE1_OFFSET) as usize;

                if !p0.is_null() && s0 > 0 { cb(std::slice::from_raw_parts(p0, s0), key); }
                if !p1.is_null() && s1 > 0 { cb(std::slice::from_raw_parts(p1, s1), false); }
                free_out(self.enc, self.outbuf.as_mut_ptr());
            }

            self.reclaimed.fill(0);
            already_used(self.enc, self.reclaimed.as_mut_ptr());
            return_one(self.enc, self.reclaimed.as_mut_ptr());
            self.inbuf.fill(0);
            get_one_in(self.enc, self.inbuf.as_mut_ptr());
        }
        Ok(())
    }
}

impl Drop for CedarEncoder {
    fn drop(&mut self) {
        unsafe {
            if self.buf_alloc {
                if let Ok(f) = self.lib_venc.get::<extern "C" fn(*mut c_void) -> i32>(
                    b"ReleaseAllocInputBuffer\0") { f(self.enc); }
            }
            if self.enc_init {
                if let Ok(f) = self.lib_venc.get::<extern "C" fn(*mut c_void)>(
                    b"VideoEncUnInit\0") { f(self.enc); }
            }
            if let Ok(f) = self.lib_venc.get::<extern "C" fn(*mut c_void)>(
                b"VideoEncDestroy\0") { f(self.enc); }
            ((*self.memops).close)();
        }
    }
}
```

---

## 9. Checklist for New Applications

- [ ] Load `libVE.so` before `libMemAdapter.so` before `libvencoder.so`
- [ ] Use `RTLD_GLOBAL` on all three `dlopen` calls
- [ ] Call `memops->open()` before `VideoEncCreate`
- [ ] Set `pVeOpsSelf = NULL` in `VencBaseConfig` (safe on H618)
- [ ] `calloc(1, 1024)` for each `VencInputBuffer`; never stack-allocate
- [ ] `calloc(1, 512)` for `VencOutputBuffer`; never stack-allocate
- [ ] Always `memset(outbuf, 0, 512)` before each `GetOneBitstreamFrame`
- [ ] Always call `AlreadyUsedInputBuffer` + `ReturnOneAllocInputBuffer` after each frame
- [ ] Check `ValidBitstreamFrameNum > 0` before calling `GetOneBitstreamFrame`
- [ ] Call `FlushCacheAllocInputBuffer` after writing NV12 data
- [ ] Do NOT call `VeInitialize` directly
- [ ] Expect `CdcIonFree` errors on teardown — they are benign
- [ ] Do not assume bitrate control works — plan for content-driven VBR
