package cast

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/discovery"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

type ClientFactory func() CastClient
type HasWiFiFn func(wifi.InterfacesFn, wifi.AddrsFn) bool

type Controller struct {
	cfg       *config.Config
	cfgPath   string
	srv       *ipc.Server
	scanner   *discovery.Scanner
	newClient ClientFactory
	hasWiFi   HasWiFiFn

	mu         sync.RWMutex
	state      string
	deviceName string
	errMsg     string

	hlsSrv   *stream.HLSServer
	ffProc   *stream.Process
	session  *Session
	cancelFn context.CancelFunc
}

func NewController(
	cfg *config.Config,
	cfgPath string,
	srv *ipc.Server,
	scanner *discovery.Scanner,
	newClient ClientFactory,
	hasWiFi HasWiFiFn,
) *Controller {
	return &Controller{
		cfg:       cfg,
		cfgPath:   cfgPath,
		srv:       srv,
		scanner:   scanner,
		newClient: newClient,
		hasWiFi:   hasWiFi,
		state:     ipc.StateIdle,
	}
}

func (c *Controller) State() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Controller) HandleCommand(cmd ipc.Command) {
	switch cmd.Cmd {
	case ipc.CmdStart, ipc.CmdSelectDevice:
		c.startPipeline(cmd.DeviceAddr, cmd.DeviceName)
	case ipc.CmdStop:
		c.stopPipeline()
		c.setState(ipc.StateIdle, "", "")
	case ipc.CmdRefreshDevices:
		go func() {
			c.mu.RLock()
			prevState := c.state
			prevDevice := c.deviceName
			prevErr := c.errMsg
			c.mu.RUnlock()
			c.setState(ipc.StateScanning, "", "")
			c.scanner.Scan()
			// Only restore if still in scanning state (not interrupted by start/stop)
			c.mu.RLock()
			curState := c.state
			c.mu.RUnlock()
			if curState == ipc.StateScanning {
				c.setState(prevState, prevDevice, prevErr)
			}
			c.pushDevices()
		}()
	case ipc.CmdGetStatus:
		c.pushCurrentState()
	case ipc.CmdSetQuality:
		c.mu.Lock()
		c.cfg.Quality = cmd.Quality
		cfgSnap := *c.cfg
		c.mu.Unlock()
		config.Save(c.cfgPath, cfgSnap)
		if c.State() == ipc.StateStreaming {
			c.restartFFmpeg()
		}
	case ipc.CmdSetAudio:
		if cmd.Audio != nil {
			c.mu.Lock()
			c.cfg.Audio = *cmd.Audio
			cfgSnap := *c.cfg
			c.mu.Unlock()
			config.Save(c.cfgPath, cfgSnap)
			if c.State() == ipc.StateStreaming {
				c.restartFFmpeg()
			}
		}
	}
}

func (c *Controller) setState(state, deviceName, errMsg string) {
	c.mu.Lock()
	c.state = state
	c.deviceName = deviceName
	c.errMsg = errMsg
	c.mu.Unlock()
	c.srv.Broadcast(ipc.Event{
		Event:      ipc.EventState,
		State:      state,
		DeviceName: deviceName,
		Error:      errMsg,
	})
}

func (c *Controller) pushCurrentState() {
	c.mu.RLock()
	ev := ipc.Event{
		Event:      ipc.EventState,
		State:      c.state,
		DeviceName: c.deviceName,
		Error:      c.errMsg,
	}
	c.mu.RUnlock()
	c.srv.Broadcast(ev)
	c.pushDevices()
}

func (c *Controller) pushDevices() {
	devs := c.scanner.Cached()
	infos := make([]ipc.DeviceInfo, len(devs))
	for i, d := range devs {
		infos[i] = ipc.DeviceInfo{Name: d.Name, Addr: d.Addr, Model: d.Model}
	}
	c.srv.Broadcast(ipc.Event{Event: ipc.EventDevices, Devices: infos})
}

func (c *Controller) startPipeline(addr, name string) {
	c.stopPipeline()

	if !c.hasWiFi(nil, nil) {
		c.setState(ipc.StateError, "", "WiFi not available")
		return
	}

	c.mu.Lock()
	c.cfg.DeviceAddr = addr
	c.cfg.DeviceName = name
	cfgSnap := *c.cfg
	c.mu.Unlock()
	config.Save(c.cfgPath, cfgSnap)

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancelFn = cancel
	c.mu.Unlock()

	go c.runPipeline(ctx, addr, name)
}

func (c *Controller) stopPipeline() {
	c.mu.Lock()
	cancel := c.cancelFn
	c.cancelFn = nil
	ffProc := c.ffProc
	c.ffProc = nil
	sess := c.session
	c.session = nil
	hlsSrv := c.hlsSrv
	c.hlsSrv = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if ffProc != nil {
		ffProc.Stop()
	}
	if sess != nil {
		sess.Stop()
	}
	if hlsSrv != nil {
		hlsSrv.Stop()
	}
	os.RemoveAll("/tmp/cast/hls")
}

func (c *Controller) runPipeline(ctx context.Context, addr, name string) {
	const hlsDir = "/tmp/cast/hls"
	const hlsAddr = ":7979"

	for attempt := 0; ; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !c.hasWiFi(nil, nil) {
			logger.Warn("controller: WiFi lost, waiting 10s")
			c.setState(ipc.StateError, name, "WiFi lost")
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				attempt = 0
				continue
			}
		}

		// Start HLS server
		hlsSrv := stream.NewHLSServer(hlsDir, hlsAddr)
		if err := hlsSrv.Start(); err != nil {
			logger.Error("controller: HLS server start: %v", err)
			c.setState(ipc.StateError, name, err.Error())
			c.sleep(ctx, attempt)
			continue
		}
		c.mu.Lock()
		c.hlsSrv = hlsSrv
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			hlsSrv.Stop()
			return
		default:
		}

		// Probe ALSA and build ffmpeg args
		c.mu.RLock()
		audioEnabled := c.cfg.Audio
		quality := c.cfg.Quality
		c.mu.RUnlock()
		alsaDev := ""
		if audioEnabled {
			alsaDev = stream.ProbeALSA(nil)
			if alsaDev == "" {
				logger.Warn("controller: no ALSA device, proceeding video-only")
			}
		}
		localIP := wifi.LocalIP(nil, nil)
		// Read physical display resolution from fb modes (not virtual_size which is multi-buffer).
		res := stream.ReadNativeResolution("/sys/class/graphics/fb0/modes")
		ffArgs := stream.BuildArgs(stream.FFmpegConfig{
			Quality:    quality,
			Audio:      audioEnabled && alsaDev != "",
			ALSADevice: alsaDev,
			Resolution: res,
			HLSDir:     hlsDir,
		})

		// Start ffmpeg
		ffProc := &stream.Process{}
		if err := ffProc.Start(hlsDir, ffArgs); err != nil {
			logger.Error("controller: ffmpeg start: %v", err)
			c.setState(ipc.StateError, name, err.Error())
			hlsSrv.Stop()
			c.mu.Lock()
			c.hlsSrv = nil
			c.mu.Unlock()
			c.sleep(ctx, attempt)
			continue
		}
		c.mu.Lock()
		c.ffProc = ffProc
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			ffProc.Stop()
			hlsSrv.Stop()
			return
		default:
		}

		// Wait briefly for HLS to produce first segments
		select {
		case <-ctx.Done():
			ffProc.Stop()
			hlsSrv.Stop()
			return
		case <-time.After(2 * time.Second):
		}

		// Connect Cast session
		c.setState(ipc.StateConnecting, name, "")
		mediaURL := "http://" + localIP + ":7979/stream.m3u8"
		sess := NewSession(c.newClient())
		if err := sess.Start(addr, mediaURL); err != nil {
			logger.Error("controller: cast session: %v", err)
			c.setState(ipc.StateError, name, err.Error())
			ffProc.Stop()
			hlsSrv.Stop()
			c.mu.Lock()
			c.ffProc = nil
			c.hlsSrv = nil
			c.mu.Unlock()
			c.sleep(ctx, attempt)
			continue
		}
		c.mu.Lock()
		c.session = sess
		c.mu.Unlock()
		c.setState(ipc.StateStreaming, name, "")
		attempt = 0
		logger.Info("controller: streaming to %s at %s", name, mediaURL)

		// Wait for ffmpeg to exit (crash = reconnect loop)
		waitCh := make(chan error, 1)
		go func() { waitCh <- ffProc.Wait() }()
		select {
		case <-ctx.Done():
			return
		case err := <-waitCh:
			logger.Warn("controller: ffmpeg exited: %v — reconnecting", err)
			c.mu.Lock()
			c.session = nil
			c.ffProc = nil
			c.hlsSrv = nil
			c.mu.Unlock()
			sess.Stop()
			hlsSrv.Stop()
			c.setState(ipc.StateError, name, "ffmpeg exited")
		}
	}
}

func (c *Controller) restartFFmpeg() {
	c.mu.RLock()
	addr := c.cfg.DeviceAddr
	name := c.cfg.DeviceName
	c.mu.RUnlock()
	c.startPipeline(addr, name)
}

func (c *Controller) sleep(ctx context.Context, attempt int) {
	select {
	case <-ctx.Done():
	case <-time.After(Backoff(attempt)):
	}
}
