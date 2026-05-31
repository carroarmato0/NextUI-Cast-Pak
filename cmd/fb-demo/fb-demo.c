/* cmd/fb-demo/fb-demo.c — animated framebuffer demo for Cedar encoder testing.
 *
 * Writes a scrolling SMPTE colour-bar pattern to /dev/fb0 at a steady frame
 * rate so that cedar-bench has consistent, non-static content to encode.
 * Every frame shifts the bars by 2 rows, forcing H.264 to do real inter-frame
 * work and producing meaningful throughput numbers.
 *
 * Supports RGB565 (16bpp) and BGRA8888 (32bpp) framebuffers.
 * Auto-detects bpp and resolution from sysfs; both can be overridden.
 *
 * Build:
 *   aarch64-linux-gnu-gcc -O2 -o fb-demo cmd/fb-demo/fb-demo.c
 *
 * Usage:
 *   fb-demo [-w WIDTH] [-h HEIGHT] [-fps N] [-duration SECS]
 *
 * Run alongside cedar-bench:
 *   fb-demo -duration 10 &
 *   cedar-bench --encoder cedar --duration 5s --quality high
 */
#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <signal.h>
#include <time.h>

static volatile int g_stop = 0;
static void handle_sig(int s) { (void)s; g_stop = 1; }

/* ── sysfs helpers ─────────────────────────────────────────────────────────── */

static int sysfs_int(const char *path, int *out)
{
    FILE *f = fopen(path, "r");
    if (!f) return -1;
    int r = fscanf(f, "%d", out);
    fclose(f);
    return r == 1 ? 0 : -1;
}

/* Parse /sys/class/graphics/fb0/modes — format: "U:WxHp-N" */
static int sysfs_display_size(int *w, int *h)
{
    FILE *f = fopen("/sys/class/graphics/fb0/modes", "r");
    if (!f) return -1;
    int r = fscanf(f, "%*[^:]:%dx%d", w, h);
    fclose(f);
    return r == 2 ? 0 : -1;
}

/* ── Colour palette — 75% SMPTE colour bars ────────────────────────────────── */

static const struct { uint8_t r, g, b; } BARS[] = {
    {191, 191, 191}, /* white  */
    {191, 191,   0}, /* yellow */
    {  0, 191, 191}, /* cyan   */
    {  0, 191,   0}, /* green  */
    {191,   0, 191}, /* magenta*/
    {191,   0,   0}, /* red    */
    {  0,   0, 191}, /* blue   */
    {  0,   0,   0}, /* black  */
};
#define NBARS ((int)(sizeof(BARS)/sizeof(BARS[0])))

static inline uint16_t to_rgb565(uint8_t r, uint8_t g, uint8_t b)
{
    return ((uint16_t)(r >> 3) << 11) | ((uint16_t)(g >> 2) << 5) | (b >> 3);
}

static inline uint32_t to_bgra(uint8_t r, uint8_t g, uint8_t b)
{
    return (uint32_t)b | ((uint32_t)g << 8) | ((uint32_t)r << 16) | 0xFF000000u;
}

/* ── Fast LCG noise for dithering overlay ──────────────────────────────────── */

static inline uint32_t lcg_next(uint32_t s) { return s * 1664525u + 1013904223u; }

/* ── Frame render ───────────────────────────────────────────────────────────── */

static void render_frame(uint8_t *buf, int w, int h, int bpp, int frame)
{
    /* Scrolling SMPTE bars with per-pixel per-frame LCG noise overlay.
     * The scroll provides global motion (2 rows/frame); the noise forces
     * unique residuals each frame so H.264 cannot predict away all content.
     * Noise amplitude ±32: visible but within the hardware encoder's
     * comfortable operating range (pure random pixels overflow the Cedar
     * hardware output buffer at 1280x720). */
    int scroll = (frame * 2) % h;
    int bar_h  = h / NBARS;
    if (bar_h < 1) bar_h = 1;
    uint32_t seed = (uint32_t)frame * 2654435761u;

    if (bpp == 16) {
        uint16_t *px = (uint16_t *)(void *)buf;
        for (int y = 0; y < h; y++) {
            int bar_idx = (((y + scroll) % h) / bar_h) % NBARS;
            int br = BARS[bar_idx].r, bg = BARS[bar_idx].g, bb = BARS[bar_idx].b;
            for (int x = 0; x < w; x++) {
                seed = lcg_next(seed);
                int d = (int)(seed >> 26) - 32;
                int r = br+d < 0 ? 0 : br+d > 255 ? 255 : br+d;
                int g = bg+d < 0 ? 0 : bg+d > 255 ? 255 : bg+d;
                int b = bb+d < 0 ? 0 : bb+d > 255 ? 255 : bb+d;
                px[y * w + x] = to_rgb565((uint8_t)r, (uint8_t)g, (uint8_t)b);
            }
        }
    } else {
        uint32_t *px = (uint32_t *)(void *)buf;
        for (int y = 0; y < h; y++) {
            int bar_idx = (((y + scroll) % h) / bar_h) % NBARS;
            int br = BARS[bar_idx].r, bg = BARS[bar_idx].g, bb = BARS[bar_idx].b;
            for (int x = 0; x < w; x++) {
                seed = lcg_next(seed);
                int d = (int)(seed >> 26) - 32;
                int r = br+d < 0 ? 0 : br+d > 255 ? 255 : br+d;
                int g = bg+d < 0 ? 0 : bg+d > 255 ? 255 : bg+d;
                int b = bb+d < 0 ? 0 : bb+d > 255 ? 255 : bb+d;
                px[y * w + x] = to_bgra((uint8_t)r, (uint8_t)g, (uint8_t)b);
            }
        }
    }
}

/* ── main ───────────────────────────────────────────────────────────────────── */

int main(int argc, char *argv[])
{
    int w          = 0;
    int h          = 0;
    int fps        = 30;
    int duration_s = 0; /* 0 = run until SIGINT/SIGTERM */

    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "-w") && i+1 < argc)        w          = atoi(argv[++i]);
        else if (!strcmp(argv[i], "-h") && i+1 < argc)   h          = atoi(argv[++i]);
        else if (!strcmp(argv[i], "-fps") && i+1 < argc) fps        = atoi(argv[++i]);
        else if (!strcmp(argv[i], "-duration") && i+1 < argc) duration_s = atoi(argv[++i]);
        else {
            fprintf(stderr,
                "Usage: fb-demo [-w W] [-h H] [-fps N] [-duration SECS]\n");
            return 1;
        }
    }

    /* Auto-detect resolution */
    if (w <= 0 || h <= 0) {
        int aw = 0, ah = 0;
        if (sysfs_display_size(&aw, &ah) == 0 && aw > 0 && ah > 0) {
            if (w <= 0) w = aw;
            if (h <= 0) h = ah;
        } else {
            fprintf(stderr, "fb-demo: can't read display size from sysfs; use -w/-h\n");
            return 1;
        }
    }

    /* Auto-detect bpp */
    int bpp = 0;
    if (sysfs_int("/sys/class/graphics/fb0/bits_per_pixel", &bpp) != 0 ||
        (bpp != 16 && bpp != 32)) {
        fprintf(stderr, "fb-demo: unsupported bpp %d\n", bpp);
        return 1;
    }

    if (fps <= 0 || fps > 120) fps = 30;
    fprintf(stderr, "fb-demo: %dx%d bpp=%d fps=%d duration=%ds\n",
            w, h, bpp, fps, duration_s);

    int fb_fd = open("/dev/fb0", O_WRONLY);
    if (fb_fd < 0) { perror("open /dev/fb0"); return 1; }

    size_t frame_bytes = (size_t)w * (size_t)h * (size_t)(bpp / 8);
    uint8_t *buf = malloc(frame_bytes);
    if (!buf) { perror("malloc"); close(fb_fd); return 1; }

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    struct timespec next;
    clock_gettime(CLOCK_MONOTONIC, &next);
    long frame_ns = 1000000000L / fps;

    int frame      = 0;
    int max_frames = (duration_s > 0) ? fps * duration_s : 0;

    while (!g_stop && (max_frames == 0 || frame < max_frames)) {
        render_frame(buf, w, h, bpp, frame);

        if (pwrite(fb_fd, buf, frame_bytes, 0) != (ssize_t)frame_bytes) {
            fprintf(stderr, "fb-demo: pwrite failed: %s\n", strerror(errno));
            break;
        }

        frame++;

        /* Pace to target fps */
        next.tv_nsec += frame_ns;
        if (next.tv_nsec >= 1000000000L) {
            next.tv_nsec -= 1000000000L;
            next.tv_sec++;
        }
        clock_nanosleep(CLOCK_MONOTONIC, TIMER_ABSTIME, &next, NULL);
    }

    free(buf);
    close(fb_fd);
    fprintf(stderr, "fb-demo: wrote %d frames\n", frame);
    return 0;
}
