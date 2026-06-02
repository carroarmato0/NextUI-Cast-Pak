package cast

import (
	"context"
	"sync"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

type Controller struct {
	cfg     *config.Config
	cfgPath string
	srv     *ipc.Server
	hasWiFi func(wifi.InterfacesFn, wifi.AddrsFn) bool

	mu         sync.RWMutex
	state      string
	deviceName string
	errMsg     string

	streamSrv *stream.StreamServer
	cancelFn  context.CancelFunc

	sessionStartedAt time.Time
}

func NewController(
	cfg *config.Config,
	cfgPath string,
	srv *ipc.Server,
	hasWiFi func(wifi.InterfacesFn, wifi.AddrsFn) bool,
) *Controller {
	return &Controller{
		cfg:     cfg,
		cfgPath: cfgPath,
		srv:     srv,
		hasWiFi: hasWiFi,
		state:   ipc.StateIdle,
	}
}

func (c *Controller) State() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *Controller) HandleCommand(cmd ipc.Command) {
	switch cmd.Cmd {
	case ipc.CmdStart:
		c.startServer()
	case ipc.CmdStop:
		c.stopServer()
	case ipc.CmdGetStatus:
		c.pushCurrentState()
	case ipc.CmdSetQuality:
		c.mu.Lock()
		changed := c.cfg.Quality != cmd.Quality
		c.cfg.Quality = cmd.Quality
		cfgSnap := *c.cfg
		c.mu.Unlock()
		config.Save(c.cfgPath, cfgSnap)
		if changed && c.State() == ipc.StateStreaming {
			c.restartFFmpeg()
		}
	case ipc.CmdSetAudio:
		if cmd.Audio != nil {
			c.mu.Lock()
			changed := c.cfg.Audio != *cmd.Audio
			c.cfg.Audio = *cmd.Audio
			cfgSnap := *c.cfg
			c.mu.Unlock()
			config.Save(c.cfgPath, cfgSnap)
			if changed && c.State() == ipc.StateStreaming {
				c.restartFFmpeg()
			}
		}
	case ipc.CmdSetEncoder:
		c.mu.Lock()
		changed := c.cfg.Encoder != cmd.Encoder
		c.cfg.Encoder = cmd.Encoder
		cfgSnap := *c.cfg
		c.mu.Unlock()
		config.Save(c.cfgPath, cfgSnap)
		if changed && c.State() == ipc.StateStreaming {
			c.restartFFmpeg()
		}
	case ipc.CmdSetLogLevel:
		c.mu.Lock()
		c.cfg.LogLevel = cmd.LogLevel
		cfgSnap := *c.cfg
		c.mu.Unlock()
		config.Save(c.cfgPath, cfgSnap)
		logger.SetLevel(logger.LevelFromString(cmd.LogLevel))
	}
}

func (c *Controller) currentStateEvent() ipc.Event {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var sessionStartedAtUnix int64
	if !c.sessionStartedAt.IsZero() {
		sessionStartedAtUnix = c.sessionStartedAt.Unix()
	}
	return ipc.Event{
		Event:            ipc.EventState,
		State:            c.state,
		DeviceName:       c.deviceName,
		Error:            c.errMsg,
		SessionStartedAt: sessionStartedAtUnix,
	}
}

func (c *Controller) setState(state, deviceName, errMsg string) {
	c.mu.Lock()
	c.state = state
	c.deviceName = deviceName
	c.errMsg = errMsg
	c.mu.Unlock()
	c.srv.Broadcast(c.currentStateEvent())
}

func (c *Controller) pushCurrentState() {
	c.srv.Broadcast(c.currentStateEvent())
}

func (c *Controller) startServer() {
	c.stopServer()

	if !c.hasWiFi(nil, nil) {
		c.setState(ipc.StateError, "", "WiFi not available")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancelFn = cancel
	c.mu.Unlock()

	go c.runServer(ctx)
}

func (c *Controller) stopServer() {
	c.mu.Lock()
	cancel := c.cancelFn
	c.cancelFn = nil
	streamSrv := c.streamSrv
	c.streamSrv = nil
	c.sessionStartedAt = time.Time{}
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if streamSrv != nil {
		streamSrv.Stop()
	}

	c.setState(ipc.StateIdle, "", "")
}

func (c *Controller) runServer(ctx context.Context) {
	const streamAddr = ":7979"

	if !c.hasWiFi(nil, nil) {
		c.setState(ipc.StateError, "", "WiFi lost")
		return
	}

	streamSrv := stream.NewStreamServer(streamAddr)
	streamSrv.OnMetrics = func(stats stream.Stats) {
		c.srv.Broadcast(ipc.Event{
			Event:          ipc.EventBitrate,
			Kbps:           stats.Kbps,
			Connected:      stats.Connected,
			LastClientAddr: stats.LastClientAddr,
			EncoderName:    stats.EncoderName,
			FFmpegStartMs:  stats.FFmpegStartMs,
			FirstByteMs:    stats.FirstByteMs,
		})
	}

	// Configure the dynamic on-demand encoder factory.
	streamSrv.GetEncoder = func() (stream.Encoder, error) {
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

		res := stream.ReadNativeResolution("/sys/class/graphics/fb0/modes")
		ffCfg := stream.FFmpegConfig{
			Quality:    quality,
			Audio:      audioEnabled && alsaDev != "",
			ALSADevice: alsaDev,
			Resolution: res,
		}
		c.mu.RLock()
		encoderPref := c.cfg.Encoder
		c.mu.RUnlock()
		logger.Info("controller: selected stream encoder with quality=%s audio=%t res=%dx%d encoder=%s", ffCfg.Quality, ffCfg.Audio, ffCfg.Resolution.X, ffCfg.Resolution.Y, encoderPref)
		return stream.NewEncoderWithPreference(ffCfg, encoderPref)
	}

	if err := streamSrv.Start(); err != nil {
		logger.Error("controller: stream server start: %v", err)
		c.setState(ipc.StateError, "", err.Error())
		return
	}
	c.mu.Lock()
	c.streamSrv = streamSrv
	c.sessionStartedAt = time.Now()
	c.mu.Unlock()

	localIP := wifi.LocalIP(nil, nil)
	serverName := localIP + ":7979"
	c.setState(ipc.StateStreaming, serverName, "")
	logger.Info("controller: DLNA Media Server started at %s", serverName)

	<-ctx.Done()
}

func (c *Controller) restartFFmpeg() {
	if c.State() == ipc.StateStreaming {
		c.startServer()
	}
}
