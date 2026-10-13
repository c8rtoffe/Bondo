//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"
)

func TestMagnetCommandQuotesExecutableAndEntireURI(t *testing.T) {
	path := `C:\Users\Example User\Bondo\bondo.exe`
	command, err := magnetCommand(path)
	if err != nil || command != `"C:\Users\Example User\Bondo\bondo.exe" --magnet "%1"` {
		t.Fatalf("protocol command: %q, %v", command, err)
	}
	for _, path := range []string{`bondo.exe`, `C:\bad"path\bondo.exe`, "C:\\bad\npath\\bondo.exe"} {
		if _, err := magnetCommand(path); err == nil {
			t.Fatalf("accepted invalid handler path %q", path)
		}
	}
}

// All mutations are under an isolated test tree, never actual magnet associations.
func registrationTestRoot(t *testing.T) (registry.Key, registry.Key) {
	t.Helper()
	path := `Software\BondoRegistrationTest-` + filepath.Base(filepath.Dir(t.TempDir()))
	root, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	classes, _, err := registry.CreateKey(root, `Software\Classes`, registry.ALL_ACCESS)
	if err != nil {
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		classes.Close()
		root.Close()
		if err := deleteRegistryTree(registry.CURRENT_USER, path); err != nil {
			t.Error(err)
		}
	})
	return root, classes
}

func TestMagnetRegistrationRefreshesEveryLaunch(t *testing.T) {
	root, classes := registrationTestRoot(t)
	executable := `C:\Bondo\bondo.exe`
	checkCommand := func(path, executable string) {
		t.Helper()
		want, _ := magnetCommand(executable)
		if got, err := registryString(root, path, ""); err != nil || got != want {
			t.Fatalf("command at %s: %q, %v", path, got, err)
		}
	}
	for i := 0; i < 3; i++ {
		if err := registerMagnetAt(root, classes, executable); err != nil {
			t.Fatal(err)
		}
		checkCommand(`Software\Classes\magnet\shell\open\command`, executable)
		checkCommand(`Software\Classes\`+magnetProgID+`\shell\open\command`, executable)
	}
	// Deleted registration is restored next time without a saved preference.
	if err := deleteRegistryTree(classes, `magnet`); err != nil {
		t.Fatal(err)
	}
	if err := registerMagnetAt(root, classes, executable); err != nil {
		t.Fatal(err)
	}
	checkCommand(`Software\Classes\magnet\shell\open\command`, executable)
	moved := `C:\New Location\bondo.exe`
	if err := registerMagnetAt(root, classes, moved); err != nil {
		t.Fatal(err)
	}
	checkCommand(`Software\Classes\magnet\shell\open\command`, moved)
	checkCommand(`Software\Classes\`+magnetProgID+`\shell\open\command`, moved)
}

func TestMagnetRegistrationKeepsExistingWindowsChoice(t *testing.T) {
	for _, scenario := range []string{"existing-handler", "user-choice"} {
		t.Run(scenario, func(t *testing.T) {
			root, classes := registrationTestRoot(t)
			path, name, value := `Software\Classes\magnet\shell\open\command`, "", `"C:\Other App\other.exe" "%1"`
			if scenario == "user-choice" {
				path, name, value = `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\magnet\UserChoice`, "ProgId", "OtherApp.Magnet"
			}
			key, _, err := registry.CreateKey(root, path, registry.ALL_ACCESS)
			if err != nil {
				t.Fatal(err)
			}
			if err := key.SetStringValue(name, value); err != nil {
				key.Close()
				t.Fatal(err)
			}
			key.Close()
			executable := `C:\Bondo\bondo.exe`
			if err := registerMagnetAt(root, classes, executable); !errors.Is(err, errMagnetDefaultApp) {
				t.Fatalf("did not respect existing choice: %v", err)
			}
			if got, err := registryString(root, path, name); err != nil || got != value {
				t.Fatalf("changed other app's choice: %q, %v", got, err)
			}
			want, _ := magnetCommand(executable)
			if got, err := registryString(root, `Software\Classes\`+magnetProgID+`\shell\open\command`, ""); err != nil || got != want {
				t.Fatalf("Bondo unavailable as a handler: %q, %v", got, err)
			}
		})
	}
}

func TestUnregisterRemovesOnlyBondoEntries(t *testing.T) {
	for _, scenario := range []string{"owned", "moved-executable", "replaced-handler", "shared-entries", "own-user-choice"} {
		t.Run(scenario, func(t *testing.T) {
			root, classes := registrationTestRoot(t)
			executable := `C:\Bondo\bondo.exe`
			if err := registerMagnetAt(root, classes, executable); err != nil {
				t.Fatal(err)
			}
			set := func(path, name, value string) {
				t.Helper()
				key, _, err := registry.CreateKey(root, path, registry.ALL_ACCESS)
				if err != nil {
					t.Fatal(err)
				}
				defer key.Close()
				if err := key.SetStringValue(name, value); err != nil {
					t.Fatal(err)
				}
			}
			assertValue := func(path, name, want string) {
				t.Helper()
				if got, err := registryString(root, path, name); err != nil || got != want {
					t.Fatalf("%s/%s: %q, %v", path, name, got, err)
				}
			}
			assertMissing := func(path string) {
				t.Helper()
				key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
				if err == nil {
					key.Close()
					t.Fatalf("key still exists: %s", path)
				}
				if err != registry.ErrNotExist {
					t.Fatal(err)
				}
			}
			set(`Software\RegisteredApplications`, "Other App", `Software\Other App\Capabilities`)
			set(`Software\Bondo`, "KeepSetting", "keep")
			choicePath := `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\magnet\UserChoice`
			set(choicePath, "ProgId", "OtherApp.Magnet")
			if scenario == "own-user-choice" {
				set(choicePath, "ProgId", magnetProgID)
			}
			otherCommand := `"C:\Other App\other.exe" "%1"`
			if scenario == "replaced-handler" {
				set(`Software\Classes\magnet\shell\open\command`, "", otherCommand)
			}
			if scenario == "shared-entries" {
				set(`Software\Classes\magnet\OpenWithProgids`, "OtherApp.Magnet", "")
				set(`Software\Classes\magnet\shell\open`, "OtherSetting", "keep")
			}
			if scenario == "moved-executable" {
				executable = `C:\Moved Bondo\bondo.exe`
			}
			for i := 0; i < 2; i++ {
				if err := unregisterMagnetAt(root, executable); err != nil {
					t.Fatal(err)
				}
			}
			assertMissing(`Software\Classes\` + magnetProgID)
			assertMissing(`Software\Bondo\Capabilities`)
			assertValue(`Software\RegisteredApplications`, "Bondo", "")
			assertValue(`Software\RegisteredApplications`, "Other App", `Software\Other App\Capabilities`)
			assertValue(`Software\Bondo`, "KeepSetting", "keep")
			if scenario == "own-user-choice" {
				assertMissing(choicePath)
			} else {
				assertValue(choicePath, "ProgId", "OtherApp.Magnet")
			}
			switch scenario {
			case "replaced-handler":
				assertValue(`Software\Classes\magnet\shell\open\command`, "", otherCommand)
				assertValue(`Software\Classes\magnet`, "", "")
				assertMissing(`Software\Classes\magnet\DefaultIcon`)
			case "shared-entries":
				key, err := registry.OpenKey(root, `Software\Classes\magnet`, registry.QUERY_VALUE)
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = key.GetStringValue("URL Protocol")
				key.Close()
				if err != nil {
					t.Fatalf("removed shared protocol marker: %v", err)
				}
				assertValue(`Software\Classes\magnet\shell\open`, "OtherSetting", "keep")
				key, err = registry.OpenKey(root, `Software\Classes\magnet\OpenWithProgids`, registry.QUERY_VALUE)
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = key.GetStringValue("OtherApp.Magnet")
				key.Close()
				if err != nil {
					t.Fatalf("removed another app's OpenWith entry: %v", err)
				}
			default:
				assertMissing(`Software\Classes\magnet`)
			}
		})
	}
}

func TestUnregisterPrunesEmptyAppKeyAndAllowsRegistrationAgain(t *testing.T) {
	root, classes := registrationTestRoot(t)
	executable := `C:\Bondo\bondo.exe`
	if err := registerMagnetAt(root, classes, executable); err != nil {
		t.Fatal(err)
	}
	if err := unregisterMagnetAt(root, executable); err != nil {
		t.Fatal(err)
	}
	key, err := registry.OpenKey(root, `Software\Bondo`, registry.QUERY_VALUE)
	if err == nil {
		key.Close()
		t.Fatal("left an empty Bondo application key")
	}
	if err != registry.ErrNotExist {
		t.Fatal(err)
	}
	if err := registerMagnetAt(root, classes, executable); err != nil {
		t.Fatal(err)
	}
	want, _ := magnetCommand(executable)
	if got, err := registryString(classes, `magnet\shell\open\command`, ""); err != nil || got != want {
		t.Fatalf("normal launch cannot register again: %q, %v", got, err)
	}
}

func TestMagnetRegistrationMigratesOwnLegacyExecutableOnly(t *testing.T) {
	for _, own := range []bool{true, false} {
		t.Run(fmt.Sprintf("own=%v", own), func(t *testing.T) {
			root, classes := registrationTestRoot(t)
			executable := `C:\Bondo\bondo.exe`
			legacy := `C:\Bondo\plexo-torrent.exe`
			if !own {
				legacy = `C:\Another Folder\plexo-torrent.exe`
			}
			command, _ := magnetCommand(legacy)
			for _, entry := range []struct{ path, value string }{
				{`Software\Classes\magnet`, "URL:Plexo-Torrent Magnet"},
				{`Software\Classes\magnet\shell\open\command`, command},
			} {
				key, _, err := registry.CreateKey(root, entry.path, registry.ALL_ACCESS)
				if err != nil {
					t.Fatal(err)
				}
				err = key.SetStringValue("", entry.value)
				key.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			err := registerMagnetAt(root, classes, executable)
			expected := command
			if own {
				if err != nil {
					t.Fatal(err)
				}
				expected, _ = magnetCommand(executable)
			} else if !errors.Is(err, errMagnetDefaultApp) {
				t.Fatalf("other installation handler: %v", err)
			}
			got, err := registryString(root, `Software\Classes\magnet\shell\open\command`, "")
			if err != nil || got != expected {
				t.Fatalf("handler %q, expected %q: %v", got, expected, err)
			}
		})
	}
}
