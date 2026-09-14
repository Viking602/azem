package agent

import (
	"github.com/Viking602/azem/internal/githubpr"
	"github.com/Viking602/venat/tool"
)

func (s *Service) nativeToolHost() *nativeHost {
	s.externalMu.Lock()
	defer s.externalMu.Unlock()
	if s.native == nil {
		s.native = newNativeHost(s.ctx)
		s.externalClosers = append(s.externalClosers, s.native.close)
	}
	return s.native
}

func (s *Service) nativeToolDrivers(root string, edit tool.Driver) []tool.Driver {
	host := s.nativeToolHost()
	drivers := []tool.Driver{
		&astDriver{root: root, operation: ToolASTGrep},
		newWebSearchDriver(s.allowNetwork),
		&githubDriver{root: root, client: githubpr.NewClient(root), networkPolicy: s.allowNetwork, allowWrite: s.allowWrite},
		&mediaDriver{root: root, operation: ToolInspectImage},
	}
	if s.allowWrite {
		drivers = append(drivers,
			&astDriver{root: root, operation: ToolASTEdit, edit: edit},
			&mediaDriver{root: root, operation: ToolGenerateImage, networkPolicy: s.allowNetwork},
			&mediaDriver{root: root, operation: ToolTTS},
		)
	}
	if s.shellPolicy != "deny" {
		drivers = append(drivers,
			&lspDriver{root: root, host: host, allowWrite: s.allowWrite, edit: edit, networkPolicy: s.allowNetwork},
			&evalDriver{root: root, host: host, approval: s.shellPolicy, networkPolicy: s.allowNetwork},
			&browserDriver{root: root, host: host, networkPolicy: s.allowNetwork},
			&debugDriver{root: root, host: host, networkPolicy: s.allowNetwork},
			&computerDriver{root: root},
		)
	}
	return drivers
}
