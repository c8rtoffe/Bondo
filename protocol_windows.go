//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var errMagnetDefaultApp = errors.New("Bondo is registered; choose Bondo for MAGNET in Windows Default apps")

const magnetProgID = "Bondo.Magnet"
const magnetProtocolSupported = true

func magnetCommand(executable string) (string, error) {
	if !filepath.IsAbs(executable) || strings.ContainsAny(executable, "\"\x00\r\n") {
		return "", fmt.Errorf("magnet handler needs an absolute executable path")
	}
	return `"` + executable + `" --magnet "%1"`, nil
}

// Register for this Windows user only; respect an existing handler/default choice.
func registerMagnetProtocol(executable string) error {
	err := registerMagnetAt(registry.CURRENT_USER, registry.CLASSES_ROOT, executable)
	if err == nil || err == errMagnetDefaultApp {
		notifyMagnetAssociationChange()
	}
	return err
}

func registerMagnetAt(root, classes registry.Key, executable string) error {
	command, err := magnetCommand(executable)
	if err != nil {
		return err
	}
	previousCommand, err := registryString(root, `Software\Classes\`+magnetProgID+`\shell\open\command`, "")
	if err != nil {
		return err
	}
	setString := func(path, name, value string) error {
		key, _, err := registry.CreateKey(root, path, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer key.Close()
		return key.SetStringValue(name, value)
	}
	for _, entry := range []struct{ path, name, value string }{
		{`Software\Classes\` + magnetProgID, "", "URL:Bondo Magnet"},
		{`Software\Classes\` + magnetProgID, "URL Protocol", ""},
		{`Software\Classes\` + magnetProgID + `\DefaultIcon`, "", `"` + executable + `",0`},
		{`Software\Classes\` + magnetProgID + `\shell\open\command`, "", command},
		{`Software\Bondo\Capabilities`, "ApplicationName", "Bondo"},
		{`Software\Bondo\Capabilities`, "ApplicationDescription", "Download magnet links with Bondo."},
		{`Software\Bondo\Capabilities`, "ApplicationIcon", `"` + executable + `",0`},
		{`Software\Bondo\Capabilities\UrlAssociations`, "magnet", magnetProgID},
		{`Software\RegisteredApplications`, "Bondo", `Software\Bondo\Capabilities`},
	} {
		if err := setString(entry.path, entry.name, entry.value); err != nil {
			return fmt.Errorf("cannot register Bondo magnet handler: %w", err)
		}
	}
	choice, choiceErr := registry.OpenKey(root, `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\magnet\UserChoice`, registry.QUERY_VALUE)
	if choiceErr == nil {
		progID, _, readErr := choice.GetStringValue("ProgId")
		choice.Close()
		if readErr != nil {
			return readErr
		}
		if !strings.EqualFold(progID, magnetProgID) && !strings.EqualFold(progID, "magnet") {
			return errMagnetDefaultApp
		}
	} else if choiceErr != registry.ErrNotExist {
		return choiceErr
	}
	// A pre-existing handler should remain the user's choice, not be replaced.
	existing, err := registry.OpenKey(classes, `magnet\shell\open\command`, registry.QUERY_VALUE)
	if err == nil {
		current, _, readErr := existing.GetStringValue("")
		existing.Close()
		if readErr != nil && readErr != registry.ErrNotExist {
			return readErr
		}
		if current != "" && current != command {
			title, err := registryString(root, `Software\Classes\magnet`, "")
			if err != nil {
				return err
			}
			legacyCommand, _ := magnetCommand(filepath.Join(filepath.Dir(executable), "plexo-torrent.exe"))
			// An older release registered only the generic scheme. Migrate this exact
			// installation's old command, never another application's handler.
			legacyOwned := title == "URL:Plexo-Torrent Magnet" && current == legacyCommand
			if !legacyOwned && (title != "URL:Bondo Magnet" || previousCommand == "" || current != previousCommand) {
				return errMagnetDefaultApp
			}
		}
	} else if err != registry.ErrNotExist {
		return err
	}
	for _, entry := range []struct{ path, name, value string }{
		{`Software\Classes\magnet`, "", "URL:Bondo Magnet"},
		{`Software\Classes\magnet`, "URL Protocol", ""},
		{`Software\Classes\magnet\DefaultIcon`, "", `"` + executable + `",0`},
		{`Software\Classes\magnet\shell\open\command`, "", command},
	} {
		if err := setString(entry.path, entry.name, entry.value); err != nil {
			return err
		}
	}
	return nil
}

func registryString(root registry.Key, path, name string) (string, error) {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err == registry.ErrNotExist {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if err == registry.ErrNotExist {
		return "", nil
	}
	return value, err
}

func notifyMagnetAssociationChange() {
	// Notify Explorer that protocol registration changed.
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	shell32.NewProc("SHChangeNotify").Call(0x08000000, 0, 0, 0)
}

func unregisterMagnetProtocol(executable string) error {
	err := unregisterMagnetAt(registry.CURRENT_USER, executable)
	// A failed cleanup can still have removed some entries; refresh Explorer too.
	notifyMagnetAssociationChange()
	return err
}

// root is injectable so tests never remove the user's actual associations.
func unregisterMagnetAt(root registry.Key, executable string) error {
	command, err := magnetCommand(executable)
	if err != nil {
		return err
	}
	ownedCommand, err := registryString(root, `Software\Classes\`+magnetProgID+`\shell\open\command`, "")
	if err != nil {
		return err
	}
	current, err := registryString(root, `Software\Classes\magnet\shell\open\command`, "")
	if err != nil {
		return err
	}
	title, err := registryString(root, `Software\Classes\magnet`, "")
	if err != nil {
		return err
	}
	var cleanupErrors []error
	record := func(err error) {
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	// Never remove another application's replacement command or shared OpenWith keys.
	if title == "URL:Bondo Magnet" || current == command || (ownedCommand != "" && current == ownedCommand) {
		record(removeOwnedMagnetScheme(root, command, ownedCommand))
	}
	choicePath := `Software\Microsoft\Windows\Shell\Associations\UrlAssociations\magnet\UserChoice`
	choice, err := registryString(root, choicePath, "ProgId")
	if err != nil {
		record(err)
	} else if strings.EqualFold(choice, magnetProgID) {
		// Only Bondo's explicit choice. Do not rewrite hashes or change key permissions.
		if err := deleteRegistryTree(root, choicePath); err != nil {
			record(fmt.Errorf("cannot clear Bondo's Windows default choice; select another MAGNET app in Windows Default apps: %w", err))
		}
	}
	for _, path := range []string{`Software\Classes\` + magnetProgID, `Software\Bondo\Capabilities`} {
		record(deleteRegistryTree(root, path))
	}
	record(removeMatchingRegistryString(root, `Software\RegisteredApplications`, "Bondo", func(value string) bool {
		return strings.EqualFold(value, `Software\Bondo\Capabilities`)
	}))
	record(deleteEmptyRegistryKey(root, `Software\Bondo`))
	return errors.Join(cleanupErrors...)
}

func removeOwnedMagnetScheme(root registry.Key, command, ownedCommand string) error {
	base := `Software\Classes\magnet`
	if err := removeMatchingRegistryString(root, base+`\shell\open\command`, "", func(value string) bool {
		return value == command || (ownedCommand != "" && value == ownedCommand)
	}); err != nil {
		return err
	}
	if err := removeMatchingRegistryString(root, base+`\DefaultIcon`, "", func(value string) bool {
		for _, candidate := range []string{command, ownedCommand} {
			if candidate != "" && value == strings.TrimSuffix(candidate, ` --magnet "%1"`)+",0" {
				return true
			}
		}
		return false
	}); err != nil {
		return err
	}
	if err := removeMatchingRegistryString(root, base, "", func(value string) bool {
		return value == "URL:Bondo Magnet"
	}); err != nil {
		return err
	}
	for _, path := range []string{base + `\shell\open\command`, base + `\shell\open`, base + `\shell`, base + `\DefaultIcon`} {
		if err := deleteEmptyRegistryKey(root, path); err != nil {
			return err
		}
	}
	key, err := registry.OpenKey(root, base, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	values, valueErr := key.ReadValueNames(-1)
	children, childErr := key.ReadSubKeyNames(-1)
	key.Close()
	if valueErr != nil {
		return valueErr
	}
	if childErr != nil {
		return childErr
	}
	// Keep the common protocol marker whenever another app still has entries.
	if len(children) == 0 && len(values) == 1 && values[0] == "URL Protocol" {
		if err := removeMatchingRegistryString(root, base, "URL Protocol", func(value string) bool { return value == "" }); err != nil {
			return err
		}
	}
	return deleteEmptyRegistryKey(root, base)
}

func removeMatchingRegistryString(root registry.Key, path, name string, matches func(string) bool) error {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.SET_VALUE)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	if matches(value) {
		return key.DeleteValue(name)
	}
	return nil
}

func deleteEmptyRegistryKey(root registry.Key, path string) error {
	key, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	values, valueErr := key.ReadValueNames(-1)
	children, childErr := key.ReadSubKeyNames(-1)
	key.Close()
	if valueErr != nil {
		return valueErr
	}
	if childErr != nil {
		return childErr
	}
	if len(values) == 0 && len(children) == 0 {
		if err := registry.DeleteKey(root, path); err != nil && err != registry.ErrNotExist {
			return err
		}
	}
	return nil
}
func deleteRegistryTree(root registry.Key, path string) error {
	key, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	children, err := key.ReadSubKeyNames(-1)
	key.Close()
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := deleteRegistryTree(root, path+`\`+child); err != nil {
			return err
		}
	}
	if err := registry.DeleteKey(root, path); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}
