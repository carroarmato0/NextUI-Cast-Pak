// internal/cast/client_test.go
package cast_test

import (
	"errors"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/cast"
)

type fakeClient struct {
	connectErr error
	loadErr    error
	stopped    bool
}

func (f *fakeClient) Connect(addr string) error { return f.connectErr }
func (f *fakeClient) Load(url, contentType string) error { return f.loadErr }
func (f *fakeClient) Stop() error { f.stopped = true; return nil }
func (f *fakeClient) Close() {}

func TestCastSession_ConnectFails(t *testing.T) {
	fake := &fakeClient{connectErr: errors.New("refused")}
	sess := cast.NewSession(fake)
	err := sess.Start("192.168.1.5:8009", "http://device/stream.m3u8")
	if err == nil {
		t.Error("expected error on connect failure")
	}
}

func TestCastSession_LoadFails(t *testing.T) {
	fake := &fakeClient{loadErr: errors.New("load error")}
	sess := cast.NewSession(fake)
	err := sess.Start("192.168.1.5:8009", "http://device/stream.m3u8")
	if err == nil {
		t.Error("expected error on load failure")
	}
}

func TestCastSession_StopCallsClient(t *testing.T) {
	fake := &fakeClient{}
	sess := cast.NewSession(fake)
	sess.Start("192.168.1.5:8009", "http://device/stream.m3u8")
	sess.Stop()
	if !fake.stopped {
		t.Error("Stop should call client.Stop()")
	}
}
