package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestZuluUIRequiresExplicitQueryAndDoubleInstallConfirmation(t *testing.T) {
	m := newModel(defaultLauncherConfig(), filepath.Join(t.TempDir(), configFileName), "", false)
	m.showZulu = true
	if m.zuluBusy || m.zuluPackage.UUID != "" || !strings.Contains(m.zuluView(), "没有联网查询") {
		t.Fatalf("opening Zulu page started work: busy=%v package=%#v", m.zuluBusy, m.zuluPackage)
	}

	querying, queryCommand := m.updateZulu(keyRune('r'))
	m = querying.(model)
	if !m.zuluBusy || queryCommand == nil || !strings.Contains(m.zuluStatus, "不会下载") {
		t.Fatalf("query state: busy=%v command=%v status=%q", m.zuluBusy, queryCommand != nil, m.zuluStatus)
	}

	pkg := ZuluPackage{UUID: "official", Name: "zulu-test.tar.gz", JavaVersion: []int{25, 0, 4}, Size: 1024}
	queried, _ := m.updateZulu(zuluMetadataMsg{pkg: pkg})
	m = queried.(model)
	first, firstCommand := m.updateZulu(keyRune('i'))
	m = first.(model)
	if firstCommand != nil || !m.confirmZuluInstall || m.zuluBusy {
		t.Fatalf("first install confirmation: confirm=%v busy=%v command=%v", m.confirmZuluInstall, m.zuluBusy, firstCommand != nil)
	}
	second, secondCommand := m.updateZulu(keyRune('i'))
	m = second.(model)
	if secondCommand == nil || !m.zuluBusy {
		t.Fatalf("second install confirmation: busy=%v command=%v", m.zuluBusy, secondCommand != nil)
	}
}

func TestZuluUIFailureDoesNotChangeJavaAndSuccessSelectsCurrentInstance(t *testing.T) {
	root := t.TempDir()
	launcher := defaultLauncherConfig()
	launcher.Instances[0].JavaPath = "old/bin/java"
	other, err := launcher.CreateInstance("other", "Other")
	if err != nil {
		t.Fatal(err)
	}
	other.JavaPath = "other/bin/java"
	m := newModel(launcher, filepath.Join(root, configFileName), "", false)
	m.showZulu = true
	m.zuluBusy = true

	failed, command := m.updateZulu(zuluInstallMsg{err: errors.New("checksum failed")})
	m = failed.(model)
	if command != nil || m.cfg.JavaPath != "old/bin/java" || m.dirty || !m.zuluStatusErr {
		t.Fatalf("failed install changed state: java=%q dirty=%v statusErr=%v", m.cfg.JavaPath, m.dirty, m.zuluStatusErr)
	}

	installedJava := filepath.Join(root, "runtimes", "zulu", "bin", javaExecutableName())
	m.zuluBusy = true
	succeeded, discoverCommand := m.updateZulu(zuluInstallMsg{result: ZuluInstallResult{JavaPath: installedJava}})
	m = succeeded.(model)
	if discoverCommand == nil || m.cfg.JavaPath != portablePath(m.cfgPath, installedJava) || !m.dirty || !m.loading {
		t.Fatalf("successful install state: java=%q dirty=%v loading=%v command=%v", m.cfg.JavaPath, m.dirty, m.loading, discoverCommand != nil)
	}
	if got := m.launcher.InstanceByID("other").JavaPath; got != "other/bin/java" {
		t.Fatalf("other instance Java changed to %q", got)
	}
}

func keyRune(character rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{character}}
}
