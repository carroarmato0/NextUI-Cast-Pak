# Cast Pak AGENT.md

This document is a handoff for future agents working on the NextUI Cast Pak. It summarizes the architecture of the Pak and gives implementation guidance for the Cedar-based encoder work so the backend can later be extracted into NextUI itself.

## Project goal

Cast is a TrimUI/NextUI Pak that mirrors the handheld display over the network using a small HTTP/DLNA-style streaming server and an on-device UI. The long-term goal is to keep the streaming backend modular enough that it can be lifted into NextUI natively later, not just remain a Pak-only implementation.

## Current architecture

### 1) UI layer

Location:
- `internal/ui/*`

Responsibilities:
- Draw the main menu, settings, and diagnostics panes.
- Show connection state, bitrate, encoder name, and other stream telemetry.
- React to controller input and send UI commands to the daemon.

Notes:
- The split layout UI is optimized for low-resource handhelds.
- The diagnostic pane at the bottom is now the primary status surface, so duplicated top-level status chrome should stay out of the way unless there is a strong reason to bring it back.
- Menu navigation must remain responsive on D-pad input; avoid changes that reintroduce excessive input repeat or jump-to-end behavior.

### 2) Controller / daemon orchestration

Location:
- `internal/cast/controller.go`

Responsibilities:
- Own the lifecycle of the streaming session.
- Persist config changes.
- Translate UI commands into server actions.
- Decide which encoder backend to instantiate.

Flow:
- UI sends an IPC command.
- Controller updates config/state.
- Controller starts or restarts the stream server.
- Controller injects a backend factory into the stream package.

Important design point:
- The controller should not know anything about Cedar internals. It should only select a backend through a factory and expose state back through IPC.

### 3) IPC layer

Location:
- `internal/ipc/*`

Responsibilities:
- Define UI → daemon commands.
- Define daemon → UI events.
- Keep the UI updated with connection state and stream metrics.

Relevant event fields:
- `connected`
- `kbps`
- `last_client_addr`
- `encoder_name`
- `ffmpeg_start_ms`
- `first_byte_ms`

Why this matters:
- Any Cedar backend should reuse the same telemetry path.
- Future UI work should not require a protocol rewrite just to show whether the encoder is software or hardware backed.

### 4) Stream server

Location:
- `internal/stream/server.go`

Responsibilities:
- Serve HTTP endpoints for the stream.
- Manage per-client lifecycle.
- Track metrics and emit them to the controller/UI.
- Own the active encoder instance.

Current abstraction:
- The server consumes a generic `Encoder` interface instead of hardcoding FFmpeg process management.

This is the key seam for Cedar:
- The server should remain transport-oriented.
- The encoder should remain a backend implementation detail.

### 5) Encoder abstraction

Location:
- `internal/stream/encoder.go`

Current contract:
- `Name() string`
- `Start(writer io.Writer) error`
- `Stop()`
- `Wait() error`

Purpose:
- Keep the backend swappable.
- Allow a process-based encoder and a native CGO-backed encoder to share the same server path.
- Make the encoder code easier to extract into NextUI later.

### 6) Software fallback encoder

Location:
- `internal/stream/ffmpeg.go`

Responsibilities:
- Build low-latency FFmpeg arguments.
- Use the current framebuffer/audio capture path.
- Act as the fallback when Cedar is unavailable or fails initialization.

Important:
- Keep this path stable. It is the safety net and the reference backend.
- The Cedar work should not break software fallback behavior.

### 7) Config and platform helpers

Location:
- `internal/config/*`
- `internal/wifi/*`

Responsibilities:
- Persist settings such as quality, audio, and log level.
- Detect Wi-Fi availability and local IP.

## Runtime flow

1. UI sends a command to the daemon.
2. Controller updates config and starts/restarts the stream server if needed.
3. Stream server creates a fresh encoder instance per session.
4. Encoder writes MPEG-TS bytes to the HTTP response.
5. Server tracks metrics and broadcasts them to the UI.
6. UI displays bitrate, latency, and encoder name in the diagnostic pane.

## Build, test, deploy

Useful commands:
- `go test ./internal/config ./internal/ipc ./internal/stream ./internal/wifi ./internal/cast`
- `./scripts/release.sh`
- `./scripts/deploy.sh`

Notes:
- The non-UI packages have been stable in tests.
- The release script packages tg5040, tg5050, and my355 variants.
- Deployment currently targets the connected ADB device and pushes the packaged Cast.pak to the device storage.

## Cedar encoder implementation guidance

This is the part future agents should focus on.

### Recommended direction

Prefer a native Cedar backend that is isolated behind the existing `Encoder` interface.

If the long-term goal is to extract this into NextUI, the Cedar backend should probably be implemented as a small native library with a stable C ABI at the boundary.

Recommendation:
- use C for the public API if possible
- use C++ only internally if RAII or richer state management genuinely simplifies the encoder implementation
- keep the exported interface C-compatible even if the internals are written in C++

Why:
- it keeps the fallback explicit
- it avoids depending on an opaque FFmpeg fork
- it is easier to extract later into a reusable NextUI subsystem
- it can be consumed from Go, C, C++, or another runtime without binding the whole backend to one language

### Suggested API shape for a reusable Cedar library

Expose a small C-style API such as:

- `cedar_probe()`
- `cedar_encoder_create(config)`
- `cedar_encoder_submit_frame(handle, frame)`
- `cedar_encoder_read_packet(handle, outbuf, cap)`
- `cedar_encoder_destroy(handle)`

The exact names can change, but the important part is the shape:
- one function to detect availability
- one function to create a session
- one function to submit a frame
- one function to read encoded bytes
- one function to destroy and release resources

The Pak’s Go code should remain a thin wrapper around that native library, while the stream server continues to see only the existing `Encoder` interface.

### Suggested file layout for the Cedar backend

A practical layout would be:

- `internal/stream/cedar/cedar.h`
  - public C API exported to Go and, later, NextUI
- `internal/stream/cedar/cedar.c`
  - small exported wrappers and entry points
- `internal/stream/cedar/cedar_internal.c`
  - vendor integration, buffer management, encode loop, cleanup
- `internal/stream/cedar/cedar_internal.h`
  - private declarations used only by the backend implementation
- `internal/stream/cedar/encoder.go`
  - Go adapter that satisfies the existing `stream.Encoder` interface

If the implementation ends up needing C++, keep it behind the same C ABI and avoid exposing C++ types in headers.

### Suggested Cedar implementation contract

Keep the native boundary intentionally small and stable. The backend should probably expose a single opaque handle plus a compact config structure.

Suggested public types:

- `typedef struct cedar_encoder cedar_encoder_t;`
- `typedef struct cedar_encoder_config cedar_encoder_config_t;`
- `typedef struct cedar_frame cedar_frame_t;`
- `typedef struct cedar_packet cedar_packet_t;`

Suggested public functions:

- `int cedar_probe(void);`
- `cedar_encoder_t *cedar_encoder_create(const cedar_encoder_config_t *cfg);`
- `int cedar_encoder_start(cedar_encoder_t *enc);`
- `int cedar_encoder_submit_frame(cedar_encoder_t *enc, const cedar_frame_t *frame);`
- `int cedar_encoder_read_packet(cedar_encoder_t *enc, cedar_packet_t *pkt);`
- `void cedar_encoder_free_packet(cedar_packet_t *pkt);`
- `void cedar_encoder_destroy(cedar_encoder_t *enc);`
- `const char *cedar_encoder_last_error(cedar_encoder_t *enc);`

Suggested config fields:
- width and height
- pixel format
- frame rate
- target bitrate
- GOP / keyframe interval
- low-latency mode flag
- audio enabled flag if the backend ever owns audio later

Suggested ownership rules:
- the caller allocates input frames
- the backend owns encoder state and any vendor resources
- the backend returns packets or byte buffers that the caller must free via the matching release function
- all cleanup must be idempotent so a failed init does not leak buffers or device handles

If the implementation uses a worker thread or ring buffer internally, keep that detail private; the external API should still look like a straightforward create/start/submit/read/destroy lifecycle.

### Device facts already confirmed

The TrimUI firmware environment exposes:
- `/dev/cedar_dev`
- vendor libraries such as:
  - `libVE.so`
  - `libvencoder.so` (CedarC v1.3.0 high-level wrapper — use this one)
  - `libvenc_h264.so`
  - `libvenc_base.so`
  - `libvenc_common.so`
  - `libMemAdapter.so`
  - `libcdc_base.so`

The generic V4L2 M2M path is not the right assumption here.

### Cedar proof-of-concept: confirmed working on H618 (TrimUI Brick)

`cmd/cedar-probe/cedar-probe.c` validates the full encode path. Exit 0 means hardware H.264 encode is working. See the source for all struct definitions and the confirmed call sequence. Key findings:

**Init sequence (exact order matters):**
1. `MemAdapterGetOpsS()` — get ScMemOpsS vtable from libMemAdapter.so
2. `GetVeOpsS(0)` — get VeOpsS vtable from libVE.so
3. `memops->open()` — open the memory adapter (must return >= 0)
4. `VideoEncCreate(VENC_CODEC_H264)` — allocates handle AND calls VeInitialize internally
5. `VideoEncInit(enc, &cfg)` — set resolution, pixel format, memops, veops
6. `AllocInputBuffer(enc, &param)` — allocate ION-backed input buffer pool
7. `GetOneAllocInputBuffer(enc, &inbuf)` — get a writable input buffer slot

**Do not call `VeInitialize` directly.** It is called internally by `VideoEncCreate`. Calling it again causes a double-init and VeRelease will segfault on cleanup.

**VencBaseConfig layout** (CedarC v1.3.0, AArch64):
```c
typedef struct {
    unsigned char   bEncH264Nalu;   /* 0 = raw Annex B; 1 = inline SPS/PPS per IDR */
    unsigned int    nInputWidth;
    unsigned int    nInputHeight;
    unsigned int    nDstWidth;
    unsigned int    nDstHeight;
    unsigned int    nStride;
    VENC_PIXEL_FMT  eInputFormat;   /* 0 = NV12/YUV420SP */
    void           *memops;         /* ScMemOpsS* from MemAdapterGetOpsS() */
    void           *veOpsS;         /* VeOpsS*    from GetVeOpsS(0) */
    void           *pVeOpsSelf;     /* pass NULL */
    unsigned char   bOnlyWbFlag;
    unsigned char   bLbcLossyComEnFlag2x;
    unsigned char   bLbcLossyComEnFlag2_5x;
    unsigned char   bIsVbvNoCache;
} VencBaseConfig;
```

**VencInputBuffer vendor layout** (TrimUI-specific — differs from upstream CedarC):
The vendor build adds three extra pointer fields after `pAddrPhyC`. The CPU-writable virtual
addresses are at offsets 40 (`_virY`) and 48 (`_virUV`), not at offsets 0/8 as in upstream.
```
offset  0: pAddrVirY   — NULL (not populated on TrimUI)
offset  8: pAddrVirC   — NULL
offset 16: pAddrPhyY   — NULL
offset 24: pAddrPhyC   — Y physical address for VE DMA
offset 32: _phyUV      — UV physical address (vendor extra)
offset 40: _virY       — Y plane CPU-writable virtual address  ← write frame data here
offset 48: _virUV      — UV plane CPU-writable virtual address ← write frame data here
offset 56: nID
offset 64: nPts
```

**VencOutputBuffer vendor layout** (differs from upstream CedarC):
```
offset  0: _flags      — int, zero
offset 16: bIsKeyFrame — int
offset 20: nTotalSize  — unsigned int, total encoded bytes  ← use this, not nSize0
offset 24: nID         — int
offset 32: pData0      — unsigned char*, first output region ← NAL data starts here
offset 40: pData1      — unsigned char*, second region (ring wrap; usually NULL)
offset 48: nSize0      — unsigned int
offset 52: nSize1      — unsigned int
```

**SPS/PPS access limitation on H618:**
`VideoEncGetParameter(enc, 16, &hdr)` returns `pBuffer` = a VE bus address with bit 32 set
(e.g. `0x100800000`). This points to VE-internal SRAM and is NOT accessible from CPU userspace:
- It is not in the process virtual address space (VeInitialize does not map VE SRAM)
- `/proc/self/maps` does not contain the address
- mmap of `/dev/cedar_dev` at the physical offset returns wrong data
- `/dev/mem` is blocked by `CONFIG_STRICT_DEVMEM`
- `VideoEncSetParameter(enc, 16, ...)` returns -1

Workaround: use a hardcoded SPS/PPS derived by bit-parsing the IDR output. For 480×272 Baseline
Level 3.0 (the probe resolution), the bytes are in `cedar-probe.c`. For real resolutions, either
parse the IDR slice header or find another extraction method (kernel module, higher-level API).

**`__EncAdapterMemGetVeAddrOffset` is a data symbol, not a function.**
`dlsym` finds it in `libvencoder.so` (returns non-NULL) but calling the pointer as a function
causes SIGBUS on AArch64. Do not call it.

**Pixel format for the probe:** NV12 / YUV420SP (`eInputFormat = 0`). Stride equals width for
the probe resolution; no extra padding needed for 480.

**Output format with `bEncH264Nalu=0`:** raw Annex B. The IDR NAL starts with `65` (nal_unit_type=5,
nal_ref_idc=3), followed by the slice header. RBSP emulation prevention bytes (00 00 03) are
present, confirming standard-compliant H.264 output.

### What the Cedar backend should do

The backend should be responsible for:
- detecting Cedar availability at runtime
- initializing the vendor stack
- allocating buffers through the vendor memory API if required
- converting frames into the format the encoder expects
- queueing frames into the H.264 encoder
- emitting bytestream output to the caller
- cleaning up all resources on disconnect or failure

### Suggested implementation shape

Add one of these under `internal/stream/`:
- `cedar.go`
- `cedar_encoder.go`
- `cedar_backend.go`

Keep it self-contained and platform-specific.

The encoder should implement the existing `Encoder` interface directly, or be wrapped so the server sees only the interface.

### Suggested selection logic

Use a selection path like this:
1. Default to FFmpeg.
2. Probe Cedar at runtime.
3. If Cedar is present and init succeeds, use it.
4. If anything fails, fall back to FFmpeg automatically.

The probe should check at minimum:
- `/dev/cedar_dev`
- required shared libraries available to the loader
- successful initialization through the vendor API

### Suggested technical approach

The missing piece is not the server plumbing; it is the encoder backend.

Open questions for the next agent:
- whether the vendor encoder should be driven via CGO or via a helper process (both viable; CGO is lower latency)
- whether framebuffer capture happens before frame submission (most likely) or inside the Cedar backend
- how to handle multi-frame streaming (the probe only encodes one frame; a streaming backend needs a continuous loop)
- whether a hardware encoder-specific SPS/PPS extraction method exists beyond the hardcoded workaround (e.g. `bEncH264Nalu=1` embeds it inline per IDR — untested for streaming)

### Strong recommendation for code organization

Keep the Cedar code separate from UI code and controller logic.

Good boundaries:
- `internal/stream/` owns encoder selection and stream plumbing.
- Cedar-specific code lives only in stream/backend files.
- UI only consumes telemetry.
- Controller only orchestrates state.

This makes the Cedar subsystem easier to extract into NextUI later.

### Performance goals

The Cedar backend should aim for:
- lower CPU usage than the software FFmpeg path
- low-latency startup
- no busy loops while idle
- clean teardown on disconnect
- minimal copies where possible

### Verification plan

After implementing Cedar support, validate the following:
- the software fallback still works
- Cedar is selected only on supported devices
- the stream starts and stops cleanly across multiple sessions
- `encoder_name` shows the active backend in the UI
- CPU usage is lower than the FFmpeg path under the same conditions
- release and deploy scripts still package correctly

## Known constraints and pitfalls

- The vendor libraries are stripped, so the init sequence may need discovery or reverse engineering.
- Different TrimUI models may expose subtle Cedar differences.
- The current UI and stream server assume the stream is available on demand; avoid changing that lifecycle unless strictly necessary.
- Do not let Cedar-specific assumptions leak into the public interfaces unless they are truly general.
- **VE SRAM SPS/PPS is not CPU-accessible on H618.** `VideoEncGetParameter(16)` returns a VE bus address (bit 32 set), not a process VA. The only reliable workaround found so far is a hardcoded SPS/PPS derived from IDR bit-parsing. Using `bEncH264Nalu=1` may embed SPS/PPS inline in the IDR frame instead — this has not been tested for continuous streaming but is worth evaluating.
- **`__EncAdapterMemGetVeAddrOffset` is a data symbol.** Do not call it via dlsym; it causes SIGBUS.
- **VencInputBuffer and VencOutputBuffer have a vendor-specific layout** that differs from upstream CedarC. Use the offsets confirmed in `cedar-probe.c`, not upstream headers.

## Suggested next task for another agent

The hardware encoder proof-of-concept is complete. `cmd/cedar-probe/cedar-probe.c` encodes one
synthetic 480×272 NV12 frame to H.264 Annex B and exits 0 on H618 (TrimUI Brick).

The next step is to integrate Cedar into the Go `Encoder` interface so the stream server can use
it instead of FFmpeg:

1. Add a Cedar backend that implements `internal/stream.Encoder`.
2. The backend should use the confirmed call sequence from `cedar-probe.c` (dlopen the vendor
   libs, open memops, create encoder, alloc buffers, submit frames in a loop, drain output).
3. Wire framebuffer capture into the frame submission loop. The probe uses a synthetic frame;
   a real backend reads from `/dev/fb0` or the UI capture path and converts to NV12.
4. Add Cedar to the selection logic in the controller: probe first, fall back to FFmpeg.
5. Measure CPU usage on-device under the same conditions as the FFmpeg path.

See the "Cedar proof-of-concept" section above for all confirmed struct layouts and pitfalls before
starting implementation.
