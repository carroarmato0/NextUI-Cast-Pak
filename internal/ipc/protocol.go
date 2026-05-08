package ipc

// Command types sent UI→daemon.
const (
	CmdStart          = "start"
	CmdStop           = "stop"
	CmdSelectDevice   = "select-device"
	CmdSetQuality     = "set-quality"
	CmdSetAudio       = "set-audio"
	CmdRefreshDevices = "refresh-devices"
	CmdGetStatus      = "get-status"
)

// Event types pushed daemon→UI.
const (
	EventState   = "state"
	EventDevices = "devices"
	EventBitrate = "bitrate"
)

// Daemon states.
const (
	StateIdle       = "idle"
	StateScanning   = "scanning"
	StateConnecting = "connecting"
	StateStreaming  = "streaming"
	StateError      = "error"
)

// Command is a UI→daemon message.
type Command struct {
	Cmd        string `json:"cmd"`
	DeviceAddr string `json:"device_addr,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	Quality    string `json:"quality,omitempty"`
	Audio      *bool  `json:"audio,omitempty"`
}

// Event is a daemon→UI push message.
type Event struct {
	Event      string       `json:"event"`
	State      string       `json:"state,omitempty"`
	DeviceName string       `json:"device_name,omitempty"`
	Error      string       `json:"error,omitempty"`
	Devices    []DeviceInfo `json:"devices,omitempty"`
	Kbps       int          `json:"kbps,omitempty"`
}

// DeviceInfo is a discovered Chromecast device.
type DeviceInfo struct {
	Name  string `json:"name"`
	Addr  string `json:"addr"`
	Model string `json:"model"`
}
