package main

import "testing"

func TestAutoProfileRecognizesMindustry(t *testing.T) {
	adapter := resolveGameAdapter(profileAuto, "mindustry.desktop.DesktopLauncher")
	if adapter.ID() != profileMindustry {
		t.Fatalf("auto profile selected %q, want %q", adapter.ID(), profileMindustry)
	}
	if adapter.DataDirectoryProperty() != "mindustry.data.dir" {
		t.Fatalf("unexpected data directory property: %q", adapter.DataDirectoryProperty())
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
