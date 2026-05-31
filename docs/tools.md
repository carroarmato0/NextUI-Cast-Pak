# Diagnostic and Benchmark Tools

Tools shipped in the `bin/<platform>/` directories for diagnosing and benchmarking the Cedar hardware encoder on device. All tools require a TrimUI/NextUI device; none work on the build host.

## Copying to device

```sh
adb push bin/tg5040/<tool> /tmp/<tool>
adb shell chmod +x /tmp/<tool>
```

Replace `tg5040` with `tg5050` (Smart Pro S) or `my355` (Brick) as needed.

---

## cedar-probe

Validates that the Cedar H.264 hardware encoder is functional on the device. Encodes one synthetic 480×272 NV12 frame and writes raw Annex-B H.264 to `/tmp/cedar-probe.h264`.

**When to use:** First thing to run on a new device or after a firmware update to confirm the vendor libraries (`libVE.so`, `libMemAdapter.so`, `libvencoder.so`) are present and working.

```sh
adb push bin/tg5040/cedar-probe /tmp/cedar-probe
adb shell chmod +x /tmp/cedar-probe
adb shell /tmp/cedar-probe
```

**Interpreting output:**

Each step logs `ok` on success or `FAIL` with a reason on error. A working device ends with:

```
PASS: wrote /tmp/cedar-probe.h264 (NNNN bytes)
```

Exit code `0` = pass, `1` = fail.

**Pulling the encoded frame for inspection:**

```sh
adb pull /tmp/cedar-probe.h264 /tmp/cedar-probe.h264
ffprobe /tmp/cedar-probe.h264
ffplay /tmp/cedar-probe.h264
```

**Common failures:**

| Log line | Cause |
|---|---|
| `dlopen(libVE.so): No such file or directory` | Vendor libs not on device or wrong path |
| `dlopen(libvencoder.so): …` | Lib present but incompatible ABI |
| `/dev/cedar_dev FAIL` | Kernel driver not loaded or no permission |
| `VideoEncCreate FAIL` | Encoder init failed — likely ION memory issue |
| `FAIL: bitstream was empty` | Encode ran but produced no output |

---

## cedar-bench

Benchmarks the Cedar hardware encoder against the FFmpeg software encoder. Reports first-byte latency and sustained throughput (kbps) for a configurable duration and quality preset. Reads live content from `/dev/fb0` — run alongside `fb-demo` for consistent, non-static input.

```sh
adb push bin/tg5040/cedar-bench /tmp/cedar-bench
adb shell chmod +x /tmp/cedar-bench
adb shell /tmp/cedar-bench [flags]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `-encoder` | `both` | `cedar`, `ffmpeg`, or `both` |
| `-duration` | `10s` | How long to run each encoder (Go duration: `5s`, `1m`) |
| `-quality` | `high` | Preset: `low`, `medium`, `high`, `ultra` |

**Example — compare both encoders for 5 seconds at high quality:**

```sh
adb shell /tmp/cedar-bench -duration 5s -quality high
```

**Example — Cedar only with motion content:**

```sh
# Terminal 1: animated framebuffer content
adb shell /tmp/fb-demo -duration 15 &

# Terminal 2: benchmark
adb shell /tmp/cedar-bench -encoder cedar -duration 10s -quality high
```

**Example output:**

```
Benchmarking cedar (high quality, 10s)...

Encoder:     cedar
Quality:     high
Duration:    10.021s
First byte:  43ms
Throughput:  312.4 kbps
Total bytes: 391823

---
Encoder:     ffmpeg
Quality:     high
Duration:    10.018s
First byte:  1204ms
First byte:  1204ms
Throughput:  295.1 kbps
Total bytes: 369882
```

**Notes:**
- On static framebuffer content (no `fb-demo`) throughput will read near 0 kbps — H.264 inter-frame prediction compresses static scenes to almost nothing; this is correct behaviour, not a bug.
- Cedar bitrate is content-driven VBR with an ~8 Mbps hardware ceiling. The `-quality` flag selects the resolution/SPS-PPS preset; it does not set a target bitrate (the vendor bitrate API is non-functional on this build — see `docs/cedar-encoder.md`).
- `cedar` reports `not available on this platform` when run on x86 or a device where `libvencoder.so` is absent; `-encoder both` silently skips Cedar in that case and runs FFmpeg only.

---

## fb-demo

Writes an animated SMPTE colour-bar pattern to `/dev/fb0` at a steady frame rate. Use it to feed motion content to `cedar-bench` or `cedar-bitrate-probe` so the encoder has real inter-frame work to do.

```sh
adb push bin/tg5040/fb-demo /tmp/fb-demo
adb shell chmod +x /tmp/fb-demo
adb shell /tmp/fb-demo [flags]
```

**Flags:**

| Flag | Default | Description |
|---|---|---|
| `-w` | auto | Frame width (auto-detected from sysfs) |
| `-h` | auto | Frame height (auto-detected from sysfs) |
| `-fps` | `30` | Target frame rate |
| `-duration` | unlimited | Stop after N seconds |

**Example — run for 30 seconds while benchmarking:**

```sh
adb shell /tmp/fb-demo -duration 30 &
adb shell /tmp/cedar-bench -duration 20s -quality ultra
```

The tool logs `fb-demo: WxH bpp=N fps=N duration=Ns` on start and `fb-demo: wrote N frames` on exit.

---

## Typical validation workflow on a new device

```sh
PLATFORM=tg5040  # or tg5050, my355

# 1. Copy tools
for t in cedar-probe cedar-bench fb-demo; do
  adb push bin/$PLATFORM/$t /tmp/$t
  adb shell chmod +x /tmp/$t
done

# 2. Confirm Cedar encoder works
adb shell /tmp/cedar-probe
# Expected last line: PASS: wrote /tmp/cedar-probe.h264 (NNNN bytes)

# 3. Benchmark with motion content
adb shell /tmp/fb-demo -duration 30 &
adb shell /tmp/cedar-bench -duration 20s -quality high
```
