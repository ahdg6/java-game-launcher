package app

import (
	"slices"
	"testing"
)

type testGameAdapter struct{ genericAdapter }

func (testGameAdapter) ID() string { return "test-game" }

func TestRegisteredAdapterCanBeConfigured(t *testing.T) {
	original := gameAdapters
	gameAdapters = append(slices.Clone(gameAdapters), testGameAdapter{})
	t.Cleanup(func() { gameAdapters = original })
	launcher := defaultLauncherConfig()
	launcher.Instances[0].GameProfile = "test-game"
	if err := normalizeLauncherConfig(&launcher); err != nil {
		t.Fatalf("registered adapter rejected by config validation: %v", err)
	}
	if adapter := resolveGameAdapter("test-game", "example.Main"); adapter.ID() != "test-game" {
		t.Fatalf("registered adapter was not selected: %s", adapter.ID())
	}
}

func TestAutoProfileRecognizesMindustry(t *testing.T) {
	adapter := resolveGameAdapter(profileAuto, "mindustry.desktop.DesktopLauncher")
	if adapter.ID() != profileMindustry {
		t.Fatalf("auto profile selected %q, want %q", adapter.ID(), profileMindustry)
	}
	if adapter.DataDirectoryProperty() != "mindustry.data.dir" {
		t.Fatalf("unexpected data directory property: %q", adapter.DataDirectoryProperty())
	}
	if modules := adapter.RequiredJavaModules("mindustry.desktop.DesktopLauncher"); len(modules) != 2 {
		t.Fatalf("unexpected required modules: %#v", modules)
	}
	if !adapter.NeedsGraphics("mindustry.desktop.DesktopLauncher") {
		t.Fatal("desktop launcher should require graphics")
	}
	if adapter.NeedsGraphics("mindustry.server.ServerLauncher") {
		t.Fatal("server launcher should not require graphics")
	}
	if !adapter.InteractiveConsole("mindustry.server.ServerLauncher") {
		t.Fatal("server launcher should expose an interactive console")
	}
	if adapter.InteractiveConsole("mindustry.desktop.DesktopLauncher") {
		t.Fatal("desktop launcher should not expose an interactive console")
	}
	jvm, game, err := adapter.LaunchArguments(AdapterLaunchContext{DataDirectory: "/games/data"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jvm) != 1 || jvm[0] != "-Dmindustry.data.dir=/games/data" || len(game) != 0 {
		t.Fatalf("unexpected adapter arguments: jvm=%#v game=%#v", jvm, game)
	}
}

func TestAutoProfileFallsBackToGeneric(t *testing.T) {
	adapter := resolveGameAdapter(profileAuto, "com.example.Game")
	if adapter.ID() != profileGeneric {
		t.Fatalf("auto profile selected %q, want %q", adapter.ID(), profileGeneric)
	}
	if adapter.DataDirectoryProperty() != "" {
		t.Fatalf("generic adapter exposed a game-specific data property")
	}
}
