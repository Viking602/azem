//go:build darwin || windows

package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	azemapp "github.com/Viking602/azem/internal/app"
	"github.com/Viking602/azem/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const sessionMenuEvent = "azem:session-menu"

func registerSessionContextMenus(app *application.App, bridge *desktop.Bridge, workspace, dataDir, configFile, language string) {
	controller := sessionMenuController{app: app, bridge: bridge, workspace: workspace, dataDir: dataDir, configFile: configFile, language: language}
	controller.register("session", false)
	controller.register("session-pinned", true)
}

type sessionMenuController struct {
	app                            *application.App
	bridge                         *desktop.Bridge
	workspace, dataDir, configFile string
	language                       string
}

func (c sessionMenuController) text(zh, en string) string {
	if c.language == "zh-CN" {
		return zh
	}
	return en
}

func (c sessionMenuController) report(err error) {
	if err != nil {
		c.app.Event.Emit(sessionMenuEvent, map[string]string{"action": "error", "error": err.Error()})
	}
}

func (c sessionMenuController) run(kind, id, decision string) {
	c.report(c.bridge.Execute(desktop.ActionRequest{Kind: kind, Target: id, Decision: decision, SessionID: id}))
}

func (c sessionMenuController) add(menu *application.ContextMenu, label string, action func(string)) {
	menu.Add(label).OnClick(func(ctx *application.Context) {
		if id := strings.TrimSpace(ctx.ContextMenuData()); id != "" {
			action(id)
		}
	})
}

func (c sessionMenuController) register(name string, pinned bool) {
	menu := c.app.ContextMenu.New()
	pinLabel := c.text("置顶聊天", "Pin Chat")
	if pinned {
		pinLabel = c.text("取消置顶聊天", "Unpin Chat")
	}
	c.add(menu, pinLabel, func(id string) { c.run(string(azemapp.ActionPinSession), id, fmt.Sprint(!pinned)) })
	c.add(menu, c.text("重命名聊天", "Rename Chat"), func(id string) {
		c.app.Event.Emit(sessionMenuEvent, map[string]string{"action": "rename", "sessionId": id})
	})
	c.add(menu, c.text("归档聊天", "Archive Chat"), func(id string) { c.run(string(azemapp.ActionArchiveSession), id, "") })
	c.add(menu, c.text("标记为未读", "Mark as Unread"), func(id string) { c.run(string(azemapp.ActionMarkSessionUnread), id, "") })
	menu.AddSeparator()
	c.add(menu, c.revealLabel(), func(string) { c.report(c.app.Env.OpenFileManager(c.workspace, false)) })
	c.add(menu, c.text("复制工作目录", "Copy Working Directory"), func(string) { c.copy(c.workspace, "working directory") })
	c.add(menu, c.text("复制会话 ID", "Copy Session ID"), func(id string) { c.copy(id, "session ID") })
	c.add(menu, c.text("复制深度链接", "Copy Deep Link"), func(id string) { c.copy(sessionDeepLink(id, c.workspace), "session deep link") })
	menu.AddSeparator()
	c.add(menu, c.text("在新聊天中继续", "Continue in New Chat"), c.continueInNewChat)
	c.add(menu, c.text("在新工作树中继续", "Continue in New Worktree"), c.continueInWorktree)
	menu.AddSeparator()
	c.add(menu, c.text("在新窗口中打开", "Open in New Window"), func(id string) {
		c.report(launchSessionWindow(c.configFile, id, c.workspace, false))
	})
	c.app.ContextMenu.Add(name, menu)
}

func (c sessionMenuController) revealLabel() string {
	if runtime.GOOS == "windows" {
		return c.text("在文件资源管理器中显示", "Show in File Explorer")
	}
	return c.text("在 Finder 中显示", "Show in Finder")
}

func (c sessionMenuController) copy(value, name string) {
	if !c.app.Clipboard.SetText(value) {
		c.report(fmt.Errorf("copy %s failed", name))
	}
}

func (c sessionMenuController) continueInNewChat(id string) {
	_, err := c.bridge.ForkSession(id, true)
	c.report(err)
}

func (c sessionMenuController) continueInWorktree(id string) {
	continuedID, err := c.bridge.ForkSession(id, false)
	if err != nil {
		c.report(err)
		return
	}
	worktree, err := azemapp.PrepareSessionWorktree(context.Background(), c.workspace, filepath.Join(c.dataDir, "session-worktrees"), continuedID)
	if err == nil {
		err = launchSessionWindow(c.configFile, continuedID, worktree.CWD, true)
	}
	if err == nil {
		return
	}
	if worktree.Path != "" {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = worktree.Remove(cleanupCtx)
		cancel()
	}
	_ = c.bridge.Execute(desktop.ActionRequest{Kind: string(azemapp.ActionArchiveSession), Target: continuedID, SessionID: continuedID})
	c.report(err)
}

func sessionWindowURL(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return "/"
	}
	return "/?session=" + url.QueryEscape(sessionID)
}

func sessionDeepLink(sessionID, workspace string) string {
	value := &url.URL{Scheme: "azem", Host: "session", Path: "/" + sessionID}
	query := value.Query()
	query.Set("workspace", workspace)
	value.RawQuery = query.Encode()
	return value.String()
}

func sessionURLFromArgs(arguments []string) string {
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "azem://") {
			return argument
		}
	}
	return ""
}

func sessionDeepLinkHandler(bridge *desktop.Bridge, window *application.WebviewWindow, workspace, configFile string) func(string) {
	return func(raw string) {
		value, err := url.Parse(raw)
		if err != nil || value.Scheme != "azem" || value.Host != "session" {
			return
		}
		id := strings.TrimPrefix(value.Path, "/")
		if !validSessionID(id) {
			return
		}
		targetWorkspace := value.Query().Get("workspace")
		if targetWorkspace != "" && filepath.Clean(targetWorkspace) != filepath.Clean(workspace) {
			if err := launchSessionWindow(configFile, id, targetWorkspace, true); err != nil {
				window.EmitEvent(sessionMenuEvent, map[string]string{"action": "error", "error": err.Error()})
			}
			return
		}
		if err := bridge.Execute(desktop.ActionRequest{Kind: string(azemapp.ActionResumeSession), Target: id, SessionID: id}); err != nil {
			window.EmitEvent(sessionMenuEvent, map[string]string{"action": "error", "error": err.Error()})
			return
		}
		window.Restore()
		window.Focus()
	}
}

func validSessionID(id string) bool {
	if strings.HasPrefix(id, "session_") {
		suffix := strings.TrimPrefix(id, "session_")
		if len(suffix) != 24 {
			return false
		}
		for _, character := range suffix {
			if !((character >= '0' && character <= '9') ||
				(character >= 'a' && character <= 'f') ||
				(character >= 'A' && character <= 'F')) {
				return false
			}
		}
		return true
	}
	if !strings.HasPrefix(id, "session-") {
		return false
	}
	suffix := strings.TrimPrefix(id, "session-")
	if len(suffix) == 0 || len(suffix) > 128 {
		return false
	}
	for _, character := range suffix {
		if !((character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			character == '-' || character == '_') {
			return false
		}
	}
	return true
}

func launchSessionWindow(configFile, sessionID, workspace string, forceWorkspace bool) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate Azem executable: %w", err)
	}
	arguments := []string{"--new-window"}
	if strings.TrimSpace(sessionID) != "" {
		arguments = append(arguments, "--session", sessionID)
	}
	if configFile != "" {
		absoluteConfig, err := filepath.Abs(configFile)
		if err != nil {
			return fmt.Errorf("resolve Azem config: %w", err)
		}
		arguments = append(arguments, "--config", absoluteConfig)
	}
	if forceWorkspace {
		arguments = append(arguments, "--workspace", workspace)
	}
	command := exec.Command(executable, arguments...)
	command.Dir = workspace
	if err := command.Start(); err != nil {
		return fmt.Errorf("open Azem window: %w", err)
	}
	return command.Process.Release()
}
