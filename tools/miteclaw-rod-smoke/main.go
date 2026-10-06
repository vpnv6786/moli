package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

func probe(binary string, fresh bool) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	args := []string{"serve", "--layout", "--host", "127.0.0.1", "--port", port, "--block-private-networks"}
	if fresh {
		args = append(args, "--fresh-geometry")
	}
	cmd := exec.Command(binary, args...)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	var endpoint string
	for endpoint == "" {
		select {
		case <-ctx.Done():
			return fmt.Errorf("startup: %w", ctx.Err())
		case err := <-done:
			return fmt.Errorf("server exited: %v", err)
		default:
		}
		response, err := client.Get("http://127.0.0.1:" + port + "/json/version")
		if err == nil {
			var version struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			json.NewDecoder(response.Body).Decode(&version)
			response.Body.Close()
			endpoint = version.WebSocketDebuggerURL
		}
		if endpoint == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	browser := rod.New().Context(ctx).ControlURL(endpoint)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("rod connect: %w", err)
	}
	page, err := browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return fmt.Errorf("create page: %w", err)
	}
	defer page.Close()
	if err := page.SetDocumentContent(`<html><head><style>body{margin:40px}button,input{display:block;width:180px;height:40px;margin:12px}</style></head><body><button id="probe-target">Save</button><input value="old"></body></html>`); err != nil {
		return err
	}
	result, err := page.Eval(`() => {
        const target=document.getElementById('probe-target'); target.getBoundingClientRect();
        const cover=document.createElement('div'); cover.style='position:fixed;inset:0;z-index:9999'; document.body.append(cover);
        target.onclick=()=>globalThis.probeClicked='target'; cover.onclick=()=>globalThis.probeClicked='cover';
        const rect=target.getBoundingClientRect(), overlay=cover.getBoundingClientRect();
        const x=rect.left+rect.width/2,y=rect.top+rect.height/2;
        globalThis.probePoint={x,y};
        return rect.width>0 && rect.height>0 && overlay.width>0 && overlay.height>0 &&
          overlay.left<=x && overlay.right>x && overlay.top<=y && overlay.bottom>y && document.elementFromPoint(x,y)===cover;
    }`)
	if err != nil {
		return fmt.Errorf("overlay eval: %w", err)
	}
	got := result.Value.Bool()
	fmt.Printf("fresh_geometry=%t overlay_capability=%t\n", fresh, got)
	if got != fresh {
		return fmt.Errorf("unexpected overlay result: want %t, got %t", fresh, got)
	}
	pointResult, err := page.Eval(`() => JSON.stringify(globalThis.probePoint)`)
	if err != nil {
		return err
	}
	var point proto.Point
	if err := json.Unmarshal([]byte(pointResult.Value.String()), &point); err != nil {
		return err
	}
	if err := page.Mouse.MoveTo(point); err != nil {
		return err
	}
	if err := page.Mouse.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	clicked, err := page.Eval(`() => globalThis.probeClicked`)
	if err != nil {
		return err
	}
	wantClicked := "target"
	if fresh {
		wantClicked = "cover"
	}
	if clicked.Value.String() != wantClicked {
		return fmt.Errorf("click target: want %s, got %s", wantClicked, clicked.Value.String())
	}
	fmt.Printf("coordinate_click_target=%s\n", wantClicked)
	var requests atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write([]byte("must not be fetched")) }))
	defer private.Close()
	if err := page.Navigate(private.URL); err == nil {
		return fmt.Errorf("private navigation succeeded")
	}
	if requests.Load() != 0 {
		return fmt.Errorf("private network guard was bypassed")
	}
	fmt.Println("private_network_block=pass")
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: moli-rod-smoke <absolute Moli binary>")
		os.Exit(2)
	}
	for _, fresh := range []bool{false, true} {
		if err := probe(os.Args[1], fresh); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
