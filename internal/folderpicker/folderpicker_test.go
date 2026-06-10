package folderpicker

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsFolderPickerUsesNativeFileDialogInProcess(t *testing.T) {
	body, err := os.ReadFile("folderpicker_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"CLSID_FileOpenDialog",
		"IID_IFileOpenDialog",
		"FOS_PICKFOLDERS",
		"COMDLG_E_CANCELLED",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("Windows folder picker should use native IFileOpenDialog; missing %q", want)
		}
	}
	if strings.Contains(s, "powershell.exe") || strings.Contains(s, "FolderBrowserDialog") {
		t.Fatal("Windows folder picker must not depend on a PowerShell FolderBrowserDialog subprocess")
	}
}
