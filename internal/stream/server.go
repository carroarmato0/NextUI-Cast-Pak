package stream

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

// ContentTyper is an optional interface for Encoder implementations that
// produce a stream format other than MPEG-TS.
type ContentTyper interface {
	ContentType() string
}

type Stats struct {
	Connected      bool
	LastClientAddr string
	EncoderName    string
	Kbps           int
	FFmpegStartMs  int
	FirstByteMs    int
	BytesSent      int64
}

type StreamServer struct {
	addr        string
	listener    net.Listener
	srv         *http.Server
	stopMu      sync.Mutex
	lastFetchMu sync.RWMutex
	lastFetchAt time.Time
	cancelFn    context.CancelFunc

	// On-demand encoder factory. GetEncoder is the preferred abstraction; the
	// older GetFFmpegCmd callback remains as a compatibility fallback for the
	// existing software path.
	GetEncoder     func() (Encoder, error)
	GetFFmpegCmd   func() (*exec.Cmd, error)
	GetContentType func() string // optional; returns MIME type for DLNA manifest
	OnMetrics      func(Stats)

	cmdMu         sync.Mutex
	activeEncoder Encoder

	metricsMu        sync.Mutex
	connected        bool
	lastClientAddr   string
	requestStartedAt time.Time
	ffmpegStartedAt  time.Time
	encoderName      string
	firstByteAt      time.Time
	bytesSent        int64
	lastEmitAt       time.Time
	lastEmitBytes    int64
}

func NewStreamServer(addr string) *StreamServer {
	return &StreamServer{
		addr: addr,
	}
}

func (s *StreamServer) LastSegmentFetchAt() time.Time {
	s.lastFetchMu.RLock()
	defer s.lastFetchMu.RUnlock()
	return s.lastFetchAt
}

func (s *StreamServer) Start() error {
	if s.listener != nil {
		return fmt.Errorf("StreamServer already started")
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", s.handler)
	mux.HandleFunc("/stream.mpg", s.handler)
	mux.HandleFunc("/stream.ts", s.handler)
	mux.HandleFunc("/stream.h264", s.handler)
	mux.HandleFunc("/stream.sdp", s.handler)
	mux.HandleFunc("/description.xml", s.descriptionHandler)
	mux.HandleFunc("/control/ContentDirectory", s.controlDirectoryHandler)
	s.srv = &http.Server{Handler: mux}

	go s.srv.Serve(ln) //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFn = cancel

	// Start SSDP service
	s.startSSDP(ctx)

	return nil
}

func (s *StreamServer) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	encoder, err := s.resolveEncoder()
	if err != nil {
		logger.Error("dms: failed to resolve encoder: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	contentType := "video/mp2t"
	if ct, ok := encoder.(ContentTyper); ok {
		contentType = ct.ContentType()
	}
	if strings.HasSuffix(r.URL.Path, ".ts") && contentType == "application/sdp" {
		http.Redirect(w, r, "/stream.sdp", http.StatusTemporaryRedirect)
		return
	}
	if strings.HasSuffix(r.URL.Path, ".sdp") {
		contentType = "application/sdp"
	} else if strings.HasSuffix(r.URL.Path, ".mp4") {
		contentType = "video/mp4"
	} else if contentType == "application/sdp" {
		contentType = "video/mp2t"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Flushing not supported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	logger.Info("dms: HTTP client connected from %s requesting %s. Spawning on-demand %s...", r.RemoteAddr, r.URL.Path, encoder.Name())
	requestStartedAt := time.Now()

	s.cmdMu.Lock()
	if s.activeEncoder != nil {
		logger.Warn("dms: terminating previous active encoder process")
		s.activeEncoder.Stop()
		_ = s.activeEncoder.Wait()
		s.activeEncoder = nil
	}
	s.activeEncoder = encoder
	s.cmdMu.Unlock()

	if strings.HasSuffix(r.URL.Path, ".sdp") {
		if rtpt, ok := encoder.(interface{ SetRTPTarget(string) }); ok {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err == nil && host != "" {
				rtpt.SetRTPTarget(host)
			}
		}

		if err := encoder.Start(io.Discard); err != nil {
			s.cmdMu.Lock()
			if s.activeEncoder == encoder {
				s.activeEncoder = nil
			}
			s.cmdMu.Unlock()
			logger.Error("dms: failed to start %s encoder for SDP manifest: %v", encoder.Name(), err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if sp, ok := encoder.(interface{ SDP() string }); ok {
			if _, err := io.WriteString(w, sp.SDP()); err != nil {
				logger.Error("dms: failed to write SDP manifest: %v", err)
			}
			flusher.Flush()
		}
		logger.Info("dms: SDP manifest served to %s", r.RemoteAddr)
		return
	}

	s.metricsMu.Lock()
	s.connected = true
	s.lastClientAddr = r.RemoteAddr
	s.requestStartedAt = requestStartedAt
	s.ffmpegStartedAt = time.Time{}
	s.encoderName = encoder.Name()
	s.firstByteAt = time.Time{}
	s.bytesSent = 0
	s.lastEmitAt = time.Time{}
	s.lastEmitBytes = 0
	s.metricsMu.Unlock()
	s.emitMetrics()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				s.emitMetrics()
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	s.lastFetchMu.Lock()
	s.lastFetchAt = requestStartedAt
	s.lastFetchMu.Unlock()

	if err := encoder.Start(&streamResponseWriter{server: s, writer: w, flusher: flusher}); err != nil {
		s.cmdMu.Lock()
		if s.activeEncoder == encoder {
			s.activeEncoder = nil
		}
		s.cmdMu.Unlock()
		logger.Error("dms: failed to start %s encoder: %v", encoder.Name(), err)
		s.metricsMu.Lock()
		s.connected = false
		s.metricsMu.Unlock()
		s.emitMetrics()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.metricsMu.Lock()
	s.ffmpegStartedAt = time.Now()
	s.metricsMu.Unlock()
	s.emitMetrics()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- encoder.Wait()
	}()

	defer func() {
		s.cmdMu.Lock()
		if s.activeEncoder == encoder {
			logger.Info("dms: closing active on-demand %s encoder...", encoder.Name())
			encoder.Stop()
			s.activeEncoder = nil
		}
		s.cmdMu.Unlock()

		s.metricsMu.Lock()
		s.connected = false
		s.metricsMu.Unlock()
		s.emitMetrics()
		logger.Info("dms: HTTP client disconnected: %s", r.RemoteAddr)
	}()

	select {
	case <-r.Context().Done():
		encoder.Stop()
		<-waitDone
	case <-waitDone:
	}
}

func (s *StreamServer) snapshotStats(now time.Time) Stats {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()

	stats := Stats{
		Connected:      s.connected,
		LastClientAddr: s.lastClientAddr,
		EncoderName:    s.encoderName,
		BytesSent:      s.bytesSent,
	}
	if !s.ffmpegStartedAt.IsZero() && !s.requestStartedAt.IsZero() {
		stats.FFmpegStartMs = int(s.ffmpegStartedAt.Sub(s.requestStartedAt) / time.Millisecond)
	}
	if !s.firstByteAt.IsZero() && !s.requestStartedAt.IsZero() {
		stats.FirstByteMs = int(s.firstByteAt.Sub(s.requestStartedAt) / time.Millisecond)
	}

	baseTime := s.firstByteAt
	baseBytes := s.bytesSent
	if baseTime.IsZero() {
		baseTime = s.requestStartedAt
	}
	if !baseTime.IsZero() {
		elapsed := now.Sub(baseTime)
		if elapsed > 0 {
			stats.Kbps = int(float64(baseBytes*8) / elapsed.Seconds() / 1000)
		}
	}

	return stats
}

func (s *StreamServer) emitMetrics() {
	if s.OnMetrics == nil {
		return
	}
	s.OnMetrics(s.snapshotStats(time.Now()))
}

func (s *StreamServer) resolveEncoder() (Encoder, error) {
	if s.GetEncoder != nil {
		return s.GetEncoder()
	}
	if s.GetFFmpegCmd != nil {
		return NewExecCmdEncoder("ffmpeg", func() (*exec.Cmd, error) {
			return s.GetFFmpegCmd()
		}), nil
	}
	return nil, fmt.Errorf("encoder callback not configured")
}

type streamResponseWriter struct {
	server  *StreamServer
	writer  io.Writer
	flusher http.Flusher
}

func (w *streamResponseWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		now := time.Now()
		firstByte := false
		w.server.lastFetchMu.Lock()
		w.server.lastFetchAt = now
		w.server.lastFetchMu.Unlock()

		w.server.metricsMu.Lock()
		if w.server.firstByteAt.IsZero() {
			w.server.firstByteAt = now
			firstByte = true
		}
		w.server.bytesSent += int64(n)
		w.server.metricsMu.Unlock()

		if w.flusher != nil {
			w.flusher.Flush()
		}
		if firstByte {
			w.server.emitMetrics()
		}
	}
	return n, err
}

func (s *StreamServer) descriptionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	xml := `<?xml version="1.0" encoding="utf-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <specVersion>
    <major>1</major>
    <minor>0</minor>
  </specVersion>
  <device>
    <deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
    <friendlyName>TrimUI Screen Cast</friendlyName>
    <manufacturer>Carroarmato0</manufacturer>
    <manufacturerURL>https://github.com/carroarmato0</manufacturerURL>
    <modelDescription>TrimUI Screen Cast DLNA Server</modelDescription>
    <modelName>TrimUI Cast Server</modelName>
    <modelNumber>1.0</modelNumber>
    <modelURL>https://github.com/carroarmato0/NextUI-Cast-Pak</modelURL>
    <UDN>uuid:fb6082d0-60b8-11e7-8cf7-a7d0e4179f53</UDN>
    <serviceList>
      <service>
        <serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>
        <serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId>
        <SCPDURL>/xml/ContentDirectory.xml</SCPDURL>
        <controlURL>/control/ContentDirectory</controlURL>
        <eventSubURL>/event/ContentDirectory</eventSubURL>
      </service>
    </serviceList>
  </device>
</root>`
	_, _ = w.Write([]byte(xml))
}

var (
	objectIDRegex   = regexp.MustCompile(`(?i)<ObjectID[^>]*>(.*?)</ObjectID>`)
	browseFlagRegex = regexp.MustCompile(`(?i)<BrowseFlag[^>]*>(.*?)</BrowseFlag>`)
)

func (s *StreamServer) resolveStreamURLAndMIME(localIP string) (url, mime string) {
	mime = "video/mp2t"
	if s.GetContentType != nil {
		mime = s.GetContentType()
	}
	switch mime {
	case "video/h264":
		url = fmt.Sprintf("http://%s:7979/stream.h264", localIP)
	case "application/sdp":
		url = fmt.Sprintf("http://%s:7979/stream.sdp", localIP)
	default:
		mime = "video/mp2t"
		url = fmt.Sprintf("http://%s:7979/stream.ts", localIP)
	}
	logger.Debug("dms: manifest resolved mime=%s url=%s", mime, url)
	return url, mime
}

func (s *StreamServer) controlDirectoryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/xml; charset=\"utf-8\"")
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Read body error", http.StatusInternalServerError)
		return
	}
	bodyStr := string(bodyBytes)

	objectID := "0"
	if matches := objectIDRegex.FindStringSubmatch(bodyStr); len(matches) > 1 {
		objectID = matches[1]
	}
	browseFlag := "BrowseDirectChildren"
	if matches := browseFlagRegex.FindStringSubmatch(bodyStr); len(matches) > 1 {
		browseFlag = matches[1]
	}

	logger.Info("dms: ContentDirectory request from %s. ObjectID: %s, BrowseFlag: %s", r.RemoteAddr, objectID, browseFlag)

	localIP := wifi.LocalIP(nil, nil)
	streamURL, streamMIME := s.resolveStreamURLAndMIME(localIP)

	var resp string
	action := r.Header.Get("SOAPAction")
	if idx := strings.Index(action, "#"); idx != -1 {
		action = strings.Trim(action[idx+1:], `"`)
	}

	if action == "" {
		if strings.Contains(bodyStr, "Browse") {
			action = "Browse"
		} else {
			action = "GetSystemUpdateID"
		}
	}

	switch action {
	case "GetSystemUpdateID":
		resp = `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:GetSystemUpdateIDResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <Id>0</Id>
    </u:GetSystemUpdateIDResponse>
  </s:Body>
</s:Envelope>`

	case "Browse":
		var didl string
		var numReturned int = 1

		if browseFlag == "BrowseMetadata" {
			if objectID == "0" {
				didl = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
  <container id="video_folder" parentID="0" restricted="1">
    <dc:title>Root</dc:title>
    <upnp:class>object.container.storageFolder</upnp:class>
  </container>
</DIDL-Lite>`
			} else if objectID == "video_folder" || objectID == "video" {
				didl = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
  <container id="video_folder" parentID="0" searchable="0" childCount="1" restricted="1">
    <dc:title>Video</dc:title>
    <upnp:class>object.container.storageFolder</upnp:class>
  </container>
</DIDL-Lite>`
			} else if objectID == "live_stream" {
				didl = fmt.Sprintf(
					`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
  <item id="live_stream" parentID="video_folder" restricted="1">
    <dc:title>TrimUI Screen Cast</dc:title>
    <upnp:class>object.item.videoItem.movie</upnp:class>
    <res protocolInfo="http-get:*:%s:*">%s</res>
  </item>
</DIDL-Lite>`, streamMIME, xmlEscape(streamURL))
			}
		} else {
			if objectID == "0" {
				didl = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
  <container id="video_folder" parentID="0" searchable="0" childCount="1" restricted="1">
    <dc:title>Video</dc:title>
    <upnp:class>object.container.storageFolder</upnp:class>
  </container>
</DIDL-Lite>`
			} else if objectID == "video_folder" || objectID == "video" {
				didl = fmt.Sprintf(
					`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">
  <item id="live_stream" parentID="video_folder" restricted="1">
    <dc:title>TrimUI Screen Cast</dc:title>
    <upnp:class>object.item.videoItem.movie</upnp:class>
    <res protocolInfo="http-get:*:%s:*">%s</res>
  </item>
</DIDL-Lite>`, streamMIME, xmlEscape(streamURL))
			} else {
				didl = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"></DIDL-Lite>`
				numReturned = 0
			}
		}

		escapedDIDL := xmlEscape(didl)
		resp = fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <Result>%s</Result>
      <NumberReturned>%d</NumberReturned>
      <TotalMatches>%d</TotalMatches>
      <UpdateID>0</UpdateID>
    </u:BrowseResponse>
  </s:Body>
</s:Envelope>`, escapedDIDL, numReturned, numReturned)

	default:
		resp = `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <s:Fault>
      <faultcode>s:Client</faultcode>
      <faultstring>UPnPError</faultstring>
      <detail>
        <UPnPError xmlns="urn:schemas-upnp-org:control-1-0">
          <errorCode>401</errorCode>
          <errorDescription>Invalid Action</errorDescription>
        </UPnPError>
      </detail>
    </s:Fault>
  </s:Body>
</s:Envelope>`
	}

	_, _ = w.Write([]byte(resp))
}

func (s *StreamServer) startSSDP(ctx context.Context) {
	localIP := wifi.LocalIP(nil, nil)
	descURL := fmt.Sprintf("http://%s:7979/description.xml", localIP)
	usn := "uuid:fb6082d0-60b8-11e7-8cf7-a7d0e4179f53::urn:schemas-upnp-org:device:MediaServer:1"

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		s.sendSSDPNotify(descURL, usn)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sendSSDPNotify(descURL, usn)
			}
		}
	}()

	go s.listenSSDPMsearch(ctx, descURL, usn)
}

func (s *StreamServer) sendSSDPNotify(descURL, usn string) {
	multicastAddr := "239.255.255.250:1900"
	addr, err := net.ResolveUDPAddr("udp4", multicastAddr)
	if err != nil {
		return
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return
	}
	defer conn.Close()

	notify := fmt.Sprintf(
		"NOTIFY * HTTP/1.1\r\n"+
			"HOST: %s\r\n"+
			"CACHE-CONTROL: max-age=1800\r\n"+
			"LOCATION: %s\r\n"+
			"NT: urn:schemas-upnp-org:device:MediaServer:1\r\n"+
			"NTS: ssdp:alive\r\n"+
			"USN: %s\r\n"+
			"\r\n",
		multicastAddr, descURL, usn,
	)
	_, _ = conn.Write([]byte(notify))
}

func (s *StreamServer) listenSSDPMsearch(ctx context.Context, descURL, usn string) {
	addr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		return
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		return
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	buf := make([]byte, 2048)
	for {
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}

		req := string(buf[:n])
		if strings.Contains(req, "M-SEARCH") &&
			(strings.Contains(req, "ssdp:all") ||
				strings.Contains(req, "urn:schemas-upnp-org:device:MediaServer:1") ||
				strings.Contains(req, "urn:schemas-upnp-org:service:ContentDirectory:1")) {

			replyConn, err := net.DialUDP("udp4", nil, raddr)
			if err != nil {
				continue
			}
			reply := fmt.Sprintf(
				"HTTP/1.1 200 OK\r\n"+
					"CACHE-CONTROL: max-age=1800\r\n"+
					"DATE: %s\r\n"+
					"EXT:\r\n"+
					"LOCATION: %s\r\n"+
					"SERVER: Linux/7.0.9 UPnP/1.0 TrimUI/1.0\r\n"+
					"ST: urn:schemas-upnp-org:device:MediaServer:1\r\n"+
					"USN: %s\r\n"+
					"\r\n",
				time.Now().Format(time.RFC1123), descURL, usn,
			)
			_, _ = replyConn.Write([]byte(reply))
			replyConn.Close()
		}
	}
}

func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func (s *StreamServer) Stop() {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	if s.cancelFn != nil {
		s.cancelFn()
	}

	s.cmdMu.Lock()
	if s.activeEncoder != nil {
		s.activeEncoder.Stop()
		_ = s.activeEncoder.Wait()
		s.activeEncoder = nil
	}
	s.cmdMu.Unlock()

	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
		s.srv = nil
		s.listener = nil
	}
}

func (s *StreamServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}
