//go:build !headless

package ui

import (
	_ "embed"
	"fmt"
	"net"
	"strings"
	"time"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"
)

const (
	menuPanelHeightRatio     = 0.28
	settingsPanelHeightRatio = 0.38
	panelOuterMargin         = int32(12)
	panelInnerPadding        = int32(12)
	panelGap                 = int32(10)
)

var splitUIFrameInterval = 33 * time.Millisecond

var splitScreenFontData []byte

//go:embed assets/fonts/HackGenConsoleNF-Bold.ttf
var _splitScreenFontData []byte

func init() {
	splitScreenFontData = _splitScreenFontData
}

type splitScreenFonts struct {
	title *ttf.Font
	body  *ttf.Font
	small *ttf.Font
}

func loadSplitScreenFonts() (*splitScreenFonts, error) {
	open := func(size int) (*ttf.Font, error) {
		rw, err := sdl.RWFromMem(splitScreenFontData)
		if err != nil {
			return nil, err
		}
		return ttf.OpenFontRW(rw, 1, size)
	}

	title, err := open(28)
	if err != nil {
		return nil, err
	}
	body, err := open(22)
	if err != nil {
		title.Close()
		return nil, err
	}
	small, err := open(18)
	if err != nil {
		title.Close()
		body.Close()
		return nil, err
	}

	return &splitScreenFonts{title: title, body: body, small: small}, nil
}

type splitRenderCache struct {
	renderer *sdl.Renderer
	text     map[string]*cachedTextTexture
	fit      map[string]string
}

type cachedTextTexture struct {
	texture *sdl.Texture
	width   int32
	height  int32
}

func newSplitRenderCache(renderer *sdl.Renderer) *splitRenderCache {
	return &splitRenderCache{
		renderer: renderer,
		text:     make(map[string]*cachedTextTexture),
		fit:      make(map[string]string),
	}
}

func (c *splitRenderCache) Close() {
	if c == nil {
		return
	}
	for key, entry := range c.text {
		if entry != nil && entry.texture != nil {
			entry.texture.Destroy()
		}
		delete(c.text, key)
	}
	for key := range c.fit {
		delete(c.fit, key)
	}
}

func (c *splitRenderCache) drawText(font *ttf.Font, text string, x, y int32, color sdl.Color) int32 {
	if c == nil {
		return 0
	}
	if font == nil || text == "" || c.renderer == nil {
		return 0
	}
	key := fmt.Sprintf("%p|%q|%d,%d,%d,%d", font, text, color.R, color.G, color.B, color.A)
	entry, ok := c.text[key]
	if !ok {
		surface, err := font.RenderUTF8Blended(text, color)
		if err != nil || surface == nil {
			return 0
		}
		defer surface.Free()

		texture, err := c.renderer.CreateTextureFromSurface(surface)
		if err != nil || texture == nil {
			return 0
		}
		entry = &cachedTextTexture{texture: texture, width: surface.W, height: surface.H}
		c.text[key] = entry
	}
	if entry == nil || entry.texture == nil {
		return 0
	}
	dst := sdl.Rect{X: x, Y: y, W: entry.width, H: entry.height}
	c.renderer.Copy(entry.texture, nil, &dst)
	return entry.height
}

func (c *splitRenderCache) fitText(font *ttf.Font, text string, maxWidth int32) string {
	if c == nil {
		return fitText(font, text, maxWidth)
	}
	if font == nil || text == "" || maxWidth <= 0 {
		return text
	}
	key := fmt.Sprintf("%p|%d|%q", font, maxWidth, text)
	if cached, ok := c.fit[key]; ok {
		return cached
	}
	value := fitText(font, text, maxWidth)
	c.fit[key] = value
	return value
}

func (f *splitScreenFonts) Close() {
	if f == nil {
		return
	}
	if f.title != nil {
		f.title.Close()
	}
	if f.body != nil {
		f.body.Close()
	}
	if f.small != nil {
		f.small.Close()
	}
}

type textBlock struct {
	text  string
	color sdl.Color
}

type diagnosticRow struct {
	label string
	value string
}

func runSplitMainMenu(a *App) {
	fonts, err := loadSplitScreenFonts()
	if err != nil {
		logger.Error("ui: failed to load split-screen fonts: %v", err)
		RunMainMenuFallback(a)
		return
	}
	defer fonts.Close()

	items := menuItems(latestState.Load().(menuState))
	selected := 0
	var navLimiter menuNavLimiter
	window := gaba.GetWindow()
	cache := newSplitRenderCache(window.Renderer)
	defer cache.Close()

	for {
		ms := latestState.Load().(menuState)
		items = menuItems(ms)
		if selected >= len(items) {
			selected = len(items) - 1
		}
		if selected < 0 {
			selected = 0
		}

		if !wifi.HasWiFi(nil, nil) {
			logger.Warn("ui: WiFi lost on menu redraw")
			gaba.ConfirmationMessage( //nolint:errcheck
				"WiFi is not connected.\nEnable WiFi before using Cast.",
				nil,
				gaba.MessageOptions{},
			)
			return
		}

		renderSplitMainMenu(a, fonts, ms, selected, items, cache)

		if event := sdl.WaitEventTimeout(int(splitUIFrameInterval / time.Millisecond)); event != nil {
			if handleSplitMainMenuEvent(a, event, &selected, items, ms, &navLimiter) {
				return
			}
		}
	}
}

func RunMainMenuFallback(a *App) {
	ms := latestState.Load().(menuState)
	statusText := statusPill(ms)
	items := menuItems(ms)

	for {
		result, err := gaba.List(gaba.DefaultListOptions(statusText, items))
		if err == gaba.ErrCancelled {
			return
		}
		if err != nil {
			return
		}
		if len(result.Selected) == 0 {
			continue
		}

		action := items[result.Selected[0]].Text
		switch action {
		case "Enable DLNA Server":
			if a.client != nil {
				a.client.Send(ipc.Command{Cmd: ipc.CmdStart}) //nolint:errcheck
				for i := 0; i < 15; i++ {
					time.Sleep(100 * time.Millisecond)
					ms = latestState.Load().(menuState)
					if ms.state == ipc.StateStreaming {
						break
					}
				}
			}
		case "Disable DLNA Server":
			if a.client != nil {
				a.client.Send(ipc.Command{Cmd: ipc.CmdStop}) //nolint:errcheck
				for i := 0; i < 15; i++ {
					time.Sleep(100 * time.Millisecond)
					ms = latestState.Load().(menuState)
					if ms.state == ipc.StateIdle {
						break
					}
				}
			}
		case "Settings":
			RunSettings(a)
		case "Quit":
			return
		}
	}
}

func handleSplitMainMenuEvent(a *App, event sdl.Event, selected *int, items []gaba.MenuItem, ms menuState, navLimiter *menuNavLimiter) bool {
	switch ev := event.(type) {
	case *sdl.QuitEvent:
		return true
	case *sdl.KeyboardEvent:
		if ev.State != sdl.PRESSED || ev.Repeat != 0 {
			return false
		}
		switch ev.Keysym.Sym {
		case sdl.K_LEFT, sdl.K_UP:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected > 0 {
				(*selected)--
			}
		case sdl.K_RIGHT, sdl.K_DOWN:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected < len(items)-1 {
				(*selected)++
			}
		case sdl.K_RETURN, sdl.K_KP_ENTER, sdl.K_SPACE:
			return activateMainMenuAction(a, selected, items, ms)
		case sdl.K_ESCAPE:
			return true
		}
	case *sdl.ControllerButtonEvent:
		if ev.State != sdl.PRESSED {
			return false
		}
		switch ev.Button {
		case sdl.CONTROLLER_BUTTON_DPAD_LEFT, sdl.CONTROLLER_BUTTON_DPAD_UP:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected > 0 {
				(*selected)--
			}
		case sdl.CONTROLLER_BUTTON_DPAD_RIGHT, sdl.CONTROLLER_BUTTON_DPAD_DOWN:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected < len(items)-1 {
				(*selected)++
			}
		case sdl.CONTROLLER_BUTTON_B, sdl.CONTROLLER_BUTTON_START:
			return activateMainMenuAction(a, selected, items, ms)
		case sdl.CONTROLLER_BUTTON_A:
			return true
		}
	case *sdl.JoyHatEvent:
		switch ev.Value {
		case sdl.HAT_LEFT, sdl.HAT_UP:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected > 0 {
				(*selected)--
			}
		case sdl.HAT_RIGHT, sdl.HAT_DOWN:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected < len(items)-1 {
				(*selected)++
			}
		}
	case *sdl.ControllerAxisEvent:
		// Simple stick support for devices mapped as axes. The thresholds are
		// intentionally conservative so the menu does not drift on noisy inputs.
		if ev.Axis == sdl.CONTROLLER_AXIS_LEFTX || ev.Axis == sdl.CONTROLLER_AXIS_LEFTY {
			switch {
			case ev.Value < -16000:
				if navLimiter != nil && !navLimiter.Allow() {
					return false
				}
				if *selected > 0 {
					(*selected)--
				}
			case ev.Value > 16000:
				if navLimiter != nil && !navLimiter.Allow() {
					return false
				}
				if *selected < len(items)-1 {
					(*selected)++
				}
			}
		}
	}
	return false
}

func activateMainMenuAction(a *App, selected *int, items []gaba.MenuItem, ms menuState) bool {
	if *selected < 0 || *selected >= len(items) {
		return false
	}

	action := items[*selected].Text
	switch action {
	case "Enable DLNA Server":
		sendMenuCommand(a, ipc.CmdStart)
	case "Disable DLNA Server":
		sendMenuCommand(a, ipc.CmdStop)
	case "Settings":
		RunSettings(a)
	case "Quit":
		return true
	}
	return false
}

func sendMenuCommand(a *App, cmd string) {
	if a == nil || a.client == nil {
		return
	}
	if err := a.client.Send(ipc.Command{Cmd: cmd}); err != nil {
		logger.Warn("ui: failed to send %s command: %v", cmd, err)
	}
}

func renderSplitMainMenu(a *App, fonts *splitScreenFonts, ms menuState, selected int, items []gaba.MenuItem, cache *splitRenderCache) {
	window := gaba.GetWindow()
	renderer := window.Renderer
	windowWidth := window.GetWidth()
	windowHeight := window.GetHeight()

	renderer.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	if window.Background != nil {
		window.RenderBackground()
	} else {
		renderer.SetDrawColor(9, 11, 16, 255)
		renderer.Clear()
	}

	availableHeight := windowHeight - (panelOuterMargin * 2)
	topHeight := int32(float64(availableHeight) * menuPanelHeightRatio)
	if topHeight < 68 {
		topHeight = 68
	}
	bottomHeight := availableHeight - topHeight - panelGap
	if bottomHeight < 132 {
		bottomHeight = 132
		topHeight = availableHeight - panelGap - bottomHeight
		if topHeight < 72 {
			topHeight = 72
			bottomHeight = availableHeight - panelGap - topHeight
		}
	}
	if bottomHeight < 100 {
		bottomHeight = availableHeight - panelGap - topHeight
	}

	topRect := sdl.Rect{X: panelOuterMargin, Y: panelOuterMargin, W: windowWidth - (panelOuterMargin * 2), H: topHeight}
	bottomRect := sdl.Rect{X: panelOuterMargin, Y: topRect.Y + topRect.H + panelGap, W: topRect.W, H: bottomHeight}

	fillPanel(renderer, &topRect, sdl.Color{R: 18, G: 22, B: 31, A: 220})
	fillPanel(renderer, &bottomRect, sdl.Color{R: 14, G: 17, B: 24, A: 230})
	drawBorder(renderer, &topRect, sdl.Color{R: 95, G: 108, B: 130, A: 255})
	drawBorder(renderer, &bottomRect, sdl.Color{R: 95, G: 108, B: 130, A: 255})

	cache.drawText(fonts.title, "Cast", topRect.X+panelInnerPadding, topRect.Y+panelInnerPadding, sdl.Color{R: 248, G: 250, B: 252, A: 255})

	menuY := topRect.Y + panelInnerPadding + int32(fonts.title.Height()) + 10
	menuGap := int32(8)
	buttonWidth := (topRect.W - panelInnerPadding*2 - menuGap*int32(len(items)-1)) / int32(len(items))
	if buttonWidth < 88 {
		buttonWidth = (topRect.W - panelInnerPadding*2 - menuGap*int32(len(items)-1)) / int32(len(items))
	}
	buttonHeight := int32(fonts.small.Height()) + 18
	for i, item := range items {
		itemX := topRect.X + panelInnerPadding + int32(i)*(buttonWidth+menuGap)
		itemRect := sdl.Rect{X: itemX, Y: menuY, W: buttonWidth, H: buttonHeight}
		label := cache.fitText(fonts.small, item.Text, buttonWidth-20)
		if i == selected {
			fillPanel(renderer, &itemRect, sdl.Color{R: 70, G: 104, B: 190, A: 220})
			drawBorder(renderer, &itemRect, sdl.Color{R: 150, G: 190, B: 255, A: 255})
			cache.drawText(fonts.small, label, itemRect.X+10, itemRect.Y+9, sdl.Color{R: 255, G: 255, B: 255, A: 255})
		} else {
			drawBorder(renderer, &itemRect, sdl.Color{R: 48, G: 56, B: 70, A: 255})
			cache.drawText(fonts.small, label, itemRect.X+10, itemRect.Y+9, sdl.Color{R: 220, G: 226, B: 236, A: 255})
		}
	}

	renderStatusPane(renderer, fonts, a, ms, bottomRect, cache)
	renderFooterHint(renderer, fonts.small, windowWidth, windowHeight, cache)
	window.Present()
}

func renderStatusPane(renderer *sdl.Renderer, fonts *splitScreenFonts, a *App, ms menuState, rect sdl.Rect, cache *splitRenderCache) {
	renderer.SetClipRect(&rect)
	defer renderer.SetClipRect(nil)

	headerY := rect.Y + panelInnerPadding
	cache.drawText(fonts.title, "Diagnostics", rect.X+panelInnerPadding, headerY, sdl.Color{R: 248, G: 250, B: 252, A: 255})
	subtitleY := headerY + int32(fonts.title.Height()) + 4
	cache.drawText(fonts.small, "Live status and latency cues", rect.X+panelInnerPadding, subtitleY, sdl.Color{R: 184, G: 193, B: 209, A: 255})

	rows := diagnosticRows(a, ms)
	gridY := subtitleY + int32(fonts.small.Height()) + 10
	gridX := rect.X + panelInnerPadding
	gridW := rect.W - panelInnerPadding*2
	columns := 2
	if rect.W < 420 || len(rows) < 3 {
		columns = 1
	}
	if columns < 1 {
		columns = 1
	}
	cellGapX := int32(10)
	cellGapY := int32(8)
	cellW := gridW
	if columns > 1 {
		cellW = (gridW - cellGapX) / 2
	}
	cellH := int32(fonts.small.Height()) + 10
	if cellH < 24 {
		cellH = 24
	}
	labelWidth := int32(72)
	if cellW < 220 {
		labelWidth = 62
	}
	rowCount := (len(rows) + columns - 1) / columns
	for row := 0; row < rowCount; row++ {
		for col := 0; col < columns; col++ {
			index := row*columns + col
			if index >= len(rows) {
				continue
			}
			cellX := gridX + int32(col)*(cellW+cellGapX)
			cellY := gridY + int32(row)*(cellH+cellGapY)
			cellRect := sdl.Rect{X: cellX, Y: cellY, W: cellW, H: cellH}
			fillPanel(renderer, &cellRect, sdl.Color{R: 20, G: 24, B: 33, A: 210})
			drawBorder(renderer, &cellRect, sdl.Color{R: 46, G: 54, B: 68, A: 255})

			rowData := rows[index]
			label := cache.fitText(fonts.small, strings.ToUpper(rowData.label), labelWidth)
			baseY := cellY + 5
			cache.drawText(fonts.small, label, cellX+10, baseY, sdl.Color{R: 124, G: 151, B: 214, A: 255})
			valueX := cellX + labelWidth + 16
			valueW := cellW - labelWidth - 26
			renderDiagnosticValue(renderer, fonts.small, rowData.value, valueX, baseY, valueW, sdl.Color{R: 236, G: 239, B: 244, A: 255}, cache)
		}
	}
}

func runSplitSettingsMenu(a *App) {
	fonts, err := loadSplitScreenFonts()
	if err != nil {
		logger.Error("ui: failed to load split-screen fonts: %v", err)
		RunMainMenuFallback(a)
		return
	}
	defer fonts.Close()

	selected := 0
	var navLimiter menuNavLimiter
	window := gaba.GetWindow()
	cache := newSplitRenderCache(window.Renderer)
	defer cache.Close()
	for {
		ms := latestState.Load().(menuState)
		if !wifi.HasWiFi(nil, nil) {
			logger.Warn("ui: WiFi lost on settings redraw")
			gaba.ConfirmationMessage( //nolint:errcheck
				"WiFi is not connected.\nEnable WiFi before using Cast.",
				nil,
				gaba.MessageOptions{},
			)
			return
		}

		renderSplitSettingsMenu(a, fonts, ms, selected, cache)

		if event := sdl.WaitEventTimeout(int(splitUIFrameInterval / time.Millisecond)); event != nil {
			if handleSplitSettingsEvent(a, event, &selected, &navLimiter) {
				return
			}
		}
	}
}

func handleSplitSettingsEvent(a *App, event sdl.Event, selected *int, navLimiter *menuNavLimiter) bool {
	switch ev := event.(type) {
	case *sdl.QuitEvent:
		return true
	case *sdl.KeyboardEvent:
		if ev.State != sdl.PRESSED || ev.Repeat != 0 {
			return false
		}
		switch ev.Keysym.Sym {
		case sdl.K_LEFT, sdl.K_UP:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected > 0 {
				(*selected)--
			}
		case sdl.K_RIGHT, sdl.K_DOWN:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected < 4 {
				(*selected)++
			}
		case sdl.K_RETURN, sdl.K_KP_ENTER, sdl.K_SPACE:
			activateSplitSettingsSelection(a, *selected)
		case sdl.K_ESCAPE, sdl.K_BACKSPACE:
			return true
		}
	case *sdl.ControllerButtonEvent:
		if ev.State != sdl.PRESSED {
			return false
		}
		switch ev.Button {
		case sdl.CONTROLLER_BUTTON_DPAD_LEFT, sdl.CONTROLLER_BUTTON_DPAD_UP:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected > 0 {
				(*selected)--
			}
		case sdl.CONTROLLER_BUTTON_DPAD_RIGHT, sdl.CONTROLLER_BUTTON_DPAD_DOWN:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected < 4 {
				(*selected)++
			}
		case sdl.CONTROLLER_BUTTON_B, sdl.CONTROLLER_BUTTON_START:
			activateSplitSettingsSelection(a, *selected)
		case sdl.CONTROLLER_BUTTON_A:
			return true
		}
	case *sdl.JoyHatEvent:
		switch ev.Value {
		case sdl.HAT_LEFT, sdl.HAT_UP:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected > 0 {
				(*selected)--
			}
		case sdl.HAT_RIGHT, sdl.HAT_DOWN:
			if navLimiter != nil && !navLimiter.Allow() {
				return false
			}
			if *selected < 4 {
				(*selected)++
			}
		}
	case *sdl.ControllerAxisEvent:
		if ev.Axis == sdl.CONTROLLER_AXIS_LEFTX || ev.Axis == sdl.CONTROLLER_AXIS_LEFTY {
			switch {
			case ev.Value < -16000:
				if navLimiter != nil && !navLimiter.Allow() {
					return false
				}
				if *selected > 0 {
					(*selected)--
				}
			case ev.Value > 16000:
				if navLimiter != nil && !navLimiter.Allow() {
					return false
				}
				if *selected < 4 {
					(*selected)++
				}
			}
		}
	}
	return false
}

func activateSplitSettingsSelection(a *App, selected int) {
	if a == nil {
		return
	}
	switch selected {
	case 0:
		next := cycleStringOption(a.cfg.Quality, []string{"low", "medium", "high", "ultra"})
		if next == "" {
			return
		}
		a.cfg.Quality = next
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logger.Error("ui: save config: %v", err)
		}
		if a.client != nil {
			a.client.Send(ipc.Command{Cmd: ipc.CmdSetQuality, Quality: next}) //nolint:errcheck
		}
	case 1:
		a.cfg.Audio = !a.cfg.Audio
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logger.Error("ui: save config: %v", err)
		}
		if a.client != nil {
			a.client.Send(ipc.Command{Cmd: ipc.CmdSetAudio, Audio: &a.cfg.Audio}) //nolint:errcheck
		}
	case 2:
		next := cycleStringOption(a.cfg.Encoder, []string{"auto", "cedar", "ffmpeg"})
		if next == "" {
			return
		}
		a.cfg.Encoder = next
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logger.Error("ui: save config: %v", err)
		}
		if a.client != nil {
			a.client.Send(ipc.Command{Cmd: ipc.CmdSetEncoder, Encoder: next}) //nolint:errcheck
		}
	case 3:
		next := cycleStringOption(a.cfg.LogLevel, []string{"info", "debug"})
		if next == "" {
			return
		}
		a.cfg.LogLevel = next
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logger.Error("ui: save config: %v", err)
		}
		if a.client != nil {
			a.client.Send(ipc.Command{Cmd: ipc.CmdSetLogLevel, LogLevel: next}) //nolint:errcheck
		}
	case 4:
		msg := fmt.Sprintf("Cast Pak\nVersion: %s\nCommit: %s", a.version, a.commit)
		gaba.ConfirmationMessage(msg, nil, gaba.MessageOptions{}) //nolint:errcheck
	}
}

func renderSplitSettingsMenu(a *App, fonts *splitScreenFonts, ms menuState, selected int, cache *splitRenderCache) {
	window := gaba.GetWindow()
	renderer := window.Renderer
	windowWidth := window.GetWidth()
	windowHeight := window.GetHeight()

	renderer.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
	if window.Background != nil {
		window.RenderBackground()
	} else {
		renderer.SetDrawColor(9, 11, 16, 255)
		renderer.Clear()
	}

	availableHeight := windowHeight - (panelOuterMargin * 2)
	topHeight := int32(float64(availableHeight) * settingsPanelHeightRatio)
	if topHeight < 92 {
		topHeight = 92
	}
	bottomHeight := availableHeight - topHeight - panelGap
	if bottomHeight < 120 {
		bottomHeight = 120
		topHeight = availableHeight - panelGap - bottomHeight
	}

	topRect := sdl.Rect{X: panelOuterMargin, Y: panelOuterMargin, W: windowWidth - (panelOuterMargin * 2), H: topHeight}
	bottomRect := sdl.Rect{X: panelOuterMargin, Y: topRect.Y + topRect.H + panelGap, W: topRect.W, H: bottomHeight}

	fillPanel(renderer, &topRect, sdl.Color{R: 18, G: 22, B: 31, A: 220})
	fillPanel(renderer, &bottomRect, sdl.Color{R: 14, G: 17, B: 24, A: 230})
	drawBorder(renderer, &topRect, sdl.Color{R: 95, G: 108, B: 130, A: 255})
	drawBorder(renderer, &bottomRect, sdl.Color{R: 95, G: 108, B: 130, A: 255})

	cache.drawText(fonts.title, "Settings", topRect.X+panelInnerPadding, topRect.Y+panelInnerPadding, sdl.Color{R: 248, G: 250, B: 252, A: 255})

	items := splitSettingsItems(a)
	cardY := topRect.Y + panelInnerPadding + int32(fonts.title.Height()) + 10
	cardGap := int32(8)
	cardW := (topRect.W - panelInnerPadding*2 - cardGap*4) / 5
	if cardW < 54 {
		cardW = 54
	}
	cardH := topRect.H - (cardY - topRect.Y) - panelInnerPadding
	if cardH < 30 {
		cardH = 30
	}
	for i, item := range items {
		cardX := topRect.X + panelInnerPadding + int32(i)*(cardW+cardGap)
		cardRect := sdl.Rect{X: cardX, Y: cardY, W: cardW, H: cardH}
		if i == selected {
			fillPanel(renderer, &cardRect, sdl.Color{R: 70, G: 104, B: 190, A: 220})
			drawBorder(renderer, &cardRect, sdl.Color{R: 150, G: 190, B: 255, A: 255})
		} else {
			fillPanel(renderer, &cardRect, sdl.Color{R: 20, G: 24, B: 33, A: 210})
			drawBorder(renderer, &cardRect, sdl.Color{R: 46, G: 54, B: 68, A: 255})
		}

		label := cache.fitText(fonts.small, strings.ToUpper(item.label), cardW-12)
		value := cache.fitText(fonts.small, item.value, cardW-12)
		labelColor := sdl.Color{R: 236, G: 239, B: 244, A: 255}
		valueColor := sdl.Color{R: 184, G: 193, B: 209, A: 255}
		if i == selected {
			valueColor = sdl.Color{R: 255, G: 255, B: 255, A: 255}
		}
		cache.drawText(fonts.small, label, cardX+6, cardY+4, labelColor)
		cache.drawText(fonts.small, value, cardX+6, cardY+cardH-6-int32(fonts.small.Height()), valueColor)
	}

	renderStatusPane(renderer, fonts, a, ms, bottomRect, cache)
	renderFooterHint(renderer, fonts.small, windowWidth, windowHeight, cache)
	window.Present()
}

func splitSettingsItems(a *App) []settingsItem {
	audio := "OFF"
	if a.cfg.Audio {
		audio = "ON"
	}
	encoder := a.cfg.Encoder
	if encoder == "" {
		encoder = "auto"
	}
	return []settingsItem{
		{label: "Quality", value: strings.ToUpper(a.cfg.Quality)},
		{label: "Audio", value: audio},
		{label: "Encoder", value: strings.ToUpper(encoder)},
		{label: "Log", value: strings.ToUpper(a.cfg.LogLevel)},
		{label: "About", value: "View"},
	}
}

func cycleStringOption(current string, options []string) string {
	for i, opt := range options {
		if opt == current {
			return options[(i+1)%len(options)]
		}
	}
	if len(options) > 0 {
		return options[0]
	}
	return ""
}

type settingsItem struct {
	label string
	value string
}

func diagnosticRows(a *App, ms menuState) []diagnosticRow {
	streamInfo := streamURL(a.cfg.DeviceAddr, ms.deviceName)
	enc := a.cfg.Encoder
	if enc == "" {
		enc = "auto"
	}
	mode := strings.ToUpper(a.cfg.Quality) + " • " + enc + " • audio " + yesNo(a.cfg.Audio)
	if ms.deviceName != "" {
		mode += " • " + ms.deviceName
	}
	clientState := "waiting"
	if ms.connected {
		clientState = "connected"
	}
	if ms.lastClientAddr != "" {
		clientState += " • " + ms.lastClientAddr
	}
	latency := "start n/a • first byte n/a"
	if ms.ffmpegStartMs > 0 || ms.firstByteMs > 0 {
		latency = fmt.Sprintf("start %d ms • first byte %d ms", ms.ffmpegStartMs, ms.firstByteMs)
	}
	rate := "waiting for a sample"
	if ms.kbps > 0 {
		rate = fmt.Sprintf("%d kbps • reconnects %d", ms.kbps, ms.reconnects)
	} else if ms.connected && ms.lastNonZeroKbps > 0 && !ms.lastNonZeroKbpsAt.IsZero() && time.Since(ms.lastNonZeroKbpsAt) < 3*time.Second {
		rate = fmt.Sprintf("%d kbps • reconnects %d", ms.lastNonZeroKbps, ms.reconnects)
	}
	uptime := "not started"
	if ms.sessionStartedAt > 0 {
		uptime = sessionAge(ms.sessionStartedAt)
	}
	return []diagnosticRow{
		{label: "State", value: stateDescription(ms)},
		{label: "Stream", value: streamInfo},
		{label: "Mode", value: mode},
		{label: "Client", value: clientState},
		{label: "Latency", value: latency},
		{label: "Rate", value: rate},
		{label: "Uptime", value: uptime},
	}
}

func stateDescription(ms menuState) string {
	switch ms.state {
	case "":
		return "Service not running"
	case ipc.StateIdle:
		return "DLNA server ready"
	case ipc.StateStreaming:
		return "DLNA server streaming"
	case ipc.StateError:
		if ms.errMsg != "" {
			return "Error: " + ms.errMsg
		}
		return "Error"
	default:
		return ms.state
	}
}

func streamURL(primaryHost, fallbackHost string) string {
	host := fallbackHost
	if host == "" {
		host = primaryHost
	}
	host = normalizeStreamHost(host)
	return "http://" + host + "/stream.ts"
}

func normalizeStreamHost(host string) string {
	if host == "" {
		return "<device-ip>:7979"
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if strings.Contains(host, ":") {
		return host
	}
	return host + ":7979"
}

func yesNo(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func fillPanel(renderer *sdl.Renderer, rect *sdl.Rect, color sdl.Color) {
	renderer.SetDrawColor(color.R, color.G, color.B, color.A)
	renderer.FillRect(rect)
}

func drawBorder(renderer *sdl.Renderer, rect *sdl.Rect, color sdl.Color) {
	renderer.SetDrawColor(color.R, color.G, color.B, color.A)
	renderer.DrawRect(rect)
}

func renderDiagnosticValue(renderer *sdl.Renderer, font *ttf.Font, text string, x, y, maxWidth int32, color sdl.Color, cache *splitRenderCache) int32 {
	if font == nil || text == "" || maxWidth <= 0 {
		return 0
	}
	if textPixelWidth(font, text) > maxWidth {
		return drawMarqueeText(renderer, font, text, x, y, maxWidth, color, cache)
	}
	if cache != nil {
		return cache.drawText(font, text, x, y, color)
	}
	return drawText(renderer, font, text, x, y, color)
}

func drawMarqueeText(renderer *sdl.Renderer, font *ttf.Font, text string, x, y, maxWidth int32, color sdl.Color, cache *splitRenderCache) int32 {
	if font == nil || text == "" || maxWidth <= 0 {
		return 0
	}
	textWidth := textPixelWidth(font, text)
	if textWidth <= maxWidth {
		if cache != nil {
			return cache.drawText(font, text, x, y, color)
		}
		return drawText(renderer, font, text, x, y, color)
	}

	gap := int32(font.Height())
	if gap < 24 {
		gap = 24
	}
	cycle := textWidth + gap
	if cycle <= 0 {
		if cache != nil {
			return cache.drawText(font, cache.fitText(font, text, maxWidth), x, y, color)
		}
		return drawText(renderer, font, fitText(font, text, maxWidth), x, y, color)
	}

	clip := sdl.Rect{X: x, Y: y, W: maxWidth, H: int32(font.Height())}
	renderer.SetClipRect(&clip)
	defer renderer.SetClipRect(nil)

	speedPxPerSec := int64(28)
	offset := int32(((time.Now().UnixMilli() * speedPxPerSec) / 1000) % int64(cycle))
	drawX := x - offset
	if cache != nil {
		cache.drawText(font, text, drawX, y, color)
		cache.drawText(font, text, drawX+cycle, y, color)
	} else {
		drawText(renderer, font, text, drawX, y, color)
		drawText(renderer, font, text, drawX+cycle, y, color)
	}
	return int32(font.Height())
}

func textPixelWidth(font *ttf.Font, text string) int32 {
	if font == nil || text == "" {
		return 0
	}
	w, _, err := font.SizeUTF8(text)
	if err != nil {
		return 0
	}
	return int32(w)
}

func drawText(renderer *sdl.Renderer, font *ttf.Font, text string, x, y int32, color sdl.Color) int32 {
	if font == nil || text == "" {
		return 0
	}
	surface, err := font.RenderUTF8Blended(text, color)
	if err != nil || surface == nil {
		return 0
	}
	defer surface.Free()

	texture, err := renderer.CreateTextureFromSurface(surface)
	if err != nil || texture == nil {
		return 0
	}
	defer texture.Destroy()

	dst := sdl.Rect{X: x, Y: y, W: surface.W, H: surface.H}
	renderer.Copy(texture, nil, &dst)
	return surface.H
}

func drawWrappedText(renderer *sdl.Renderer, font *ttf.Font, text string, x, y, maxWidth int32, color sdl.Color) int32 {
	if font == nil || text == "" || maxWidth <= 0 {
		return 0
	}
	surface, err := font.RenderUTF8BlendedWrapped(text, color, int(maxWidth))
	if err != nil || surface == nil {
		return 0
	}
	defer surface.Free()

	texture, err := renderer.CreateTextureFromSurface(surface)
	if err != nil || texture == nil {
		return 0
	}
	defer texture.Destroy()

	dst := sdl.Rect{X: x, Y: y, W: surface.W, H: surface.H}
	renderer.Copy(texture, nil, &dst)
	return surface.H
}

func fitText(font *ttf.Font, text string, maxWidth int32) string {
	if font == nil || text == "" || maxWidth <= 0 {
		return text
	}
	w, _, err := font.SizeUTF8(text)
	if err == nil && int32(w) <= maxWidth {
		return text
	}
	ellipsis := "..."
	ew, _, err := font.SizeUTF8(ellipsis)
	if err != nil || int32(ew) > maxWidth {
		return ""
	}
	runes := []rune(text)
	lo, hi := 0, len(runes)
	best := ellipsis
	for lo <= hi {
		mid := (lo + hi) / 2
		candidate := string(runes[:mid]) + ellipsis
		cw, _, err := font.SizeUTF8(candidate)
		if err != nil {
			hi = mid - 1
			continue
		}
		if int32(cw) <= maxWidth {
			best = candidate
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

func truncateString(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

type menuNavLimiter struct {
	nextAllowed time.Time
}

func (m *menuNavLimiter) Allow() bool {
	if m == nil {
		return true
	}
	now := time.Now()
	if now.Before(m.nextAllowed) {
		return false
	}
	m.nextAllowed = now.Add(140 * time.Millisecond)
	return true
}

func renderFooterHint(renderer *sdl.Renderer, font *ttf.Font, windowWidth, windowHeight int32, cache *splitRenderCache) {
	if font == nil {
		return
	}
	hint := "A/Start: confirm   B: back/exit"
	if h := font.Height(); h > 0 {
		if cache != nil {
			cache.drawText(font, hint, panelOuterMargin, windowHeight-panelOuterMargin-int32(h), sdl.Color{R: 160, G: 170, B: 188, A: 255})
		} else {
			drawText(renderer, font, hint, panelOuterMargin, windowHeight-panelOuterMargin-int32(h), sdl.Color{R: 160, G: 170, B: 188, A: 255})
		}
	}
	_ = windowWidth
}
