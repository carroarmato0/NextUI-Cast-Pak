package cast

import (
	"net"
	"strconv"

	"github.com/vishen/go-chromecast/application"
)

// CastClient abstracts a Chromecast connection for testing and real use.
type CastClient interface {
	Connect(addr string) error
	Load(url, contentType string) error
	Stop() error
	Close()
}

// chromeCastClient is the real CastClient backed by go-chromecast.
type chromeCastClient struct {
	app *application.Application
}

// NewRealClient returns a CastClient backed by the go-chromecast library.
func NewRealClient() CastClient {
	return &chromeCastClient{
		app: application.NewApplication(),
	}
}

// Connect establishes a connection to the Chromecast at the given addr (host:port).
func (c *chromeCastClient) Connect(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}
	return c.app.Start(host, port)
}

// Load instructs the Chromecast to play the given URL with the given content type.
func (c *chromeCastClient) Load(url, contentType string) error {
	// startTime=0, transcode=false, detach=false, forceDetach=false
	return c.app.Load(url, 0, contentType, false, false, false)
}

// Stop stops the current media on the Chromecast.
func (c *chromeCastClient) Stop() error {
	return c.app.Stop()
}

// Close closes the connection to the Chromecast.
func (c *chromeCastClient) Close() {
	c.app.Close(false) //nolint:errcheck
}

// Session manages a single cast session lifecycle.
type Session struct {
	client CastClient
}

// NewSession creates a new Session using the provided CastClient.
func NewSession(client CastClient) *Session {
	return &Session{client: client}
}

// Start connects to the Chromecast at addr and begins streaming mediaURL.
// The content type used is "application/x-mpegURL" (HLS).
// On load failure the connection is closed before returning the error.
func (s *Session) Start(addr, mediaURL string) error {
	if err := s.client.Connect(addr); err != nil {
		return err
	}
	if err := s.client.Load(mediaURL, "application/x-mpegURL"); err != nil {
		s.client.Close()
		return err
	}
	return nil
}

// Stop halts playback and closes the connection.
func (s *Session) Stop() {
	s.client.Stop() //nolint:errcheck
	s.client.Close()
}
