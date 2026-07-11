package codexdesktop

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDetectedFromPowerShellOutputStates(t *testing.T) {
	for _, tc := range []struct {
		name       string
		json       string
		wantStatus Status
		wantErr    error
		installed  bool
	}{
		{
			name:       "ready chatgpt",
			json:       `{"status":"ready","installed":true,"version":"1.2.3","package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\ChatGPT","app_user_model_id":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0!ChatGPT","scheme_registered":true,"scheme_target_valid":true}`,
			wantStatus: StatusReady,
			installed:  true,
		},
		{
			name:       "ready legacy codex",
			json:       `{"status":"ready","installed":true,"version":"0.9.0","package_family_name":"OpenAI.Codex_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\Codex","app_user_model_id":"OpenAI.Codex_2p2nqsd0c76g0!Codex","scheme_registered":true,"scheme_target_valid":true}`,
			wantStatus: StatusReady,
			installed:  true,
		},
		{
			name:       "not installed",
			json:       `{"status":"not_installed","installed":false,"scheme_registered":false,"scheme_target_valid":false}`,
			wantStatus: StatusNotInstalled,
			wantErr:    ErrNotFound,
		},
		{
			name:       "scheme missing",
			json:       `{"status":"scheme_missing","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\ChatGPT","scheme_registered":false,"scheme_target_valid":false}`,
			wantStatus: StatusSchemeMissing,
			wantErr:    ErrSchemeMissing,
			installed:  true,
		},
		{
			name:       "scheme target invalid",
			json:       `{"status":"scheme_target_invalid","installed":true,"package_family_name":"OpenAI.Codex_2p2nqsd0c76g0","install_location":"C:\\Program Files\\WindowsApps\\Codex","scheme_registered":true,"scheme_target_valid":false}`,
			wantStatus: StatusSchemeTargetInvalid,
			wantErr:    ErrSchemeTargetInvalid,
			installed:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			det, err := detectedFromPowerShellOutput([]byte(tc.json+"\r\n"), nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if det.Status != tc.wantStatus || det.Installed != tc.installed {
				t.Fatalf("det=%+v, want status=%q installed=%t", det, tc.wantStatus, tc.installed)
			}
		})
	}
}

func TestParseAppUserModelID(t *testing.T) {
	for _, tc := range []struct {
		name              string
		aumid             string
		wantPackageFamily string
		wantApplicationID string
		wantErr           bool
	}{
		{
			name:              "chatgpt",
			aumid:             ChatGPTPackageFamily + "!ChatGPT.App",
			wantPackageFamily: ChatGPTPackageFamily,
			wantApplicationID: "ChatGPT.App",
		},
		{
			name:              "legacy codex",
			aumid:             LegacyCodexPackageFamily + "!Codex",
			wantPackageFamily: LegacyCodexPackageFamily,
			wantApplicationID: "Codex",
		},
		{name: "empty", wantErr: true},
		{name: "missing separator", aumid: ChatGPTPackageFamily, wantErr: true},
		{name: "empty package family", aumid: "!ChatGPT", wantErr: true},
		{name: "empty application id", aumid: ChatGPTPackageFamily + "!", wantErr: true},
		{name: "multiple separators", aumid: ChatGPTPackageFamily + "!ChatGPT!Other", wantErr: true},
		{name: "surrounding whitespace", aumid: " " + ChatGPTPackageFamily + "!ChatGPT", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packageFamily, applicationID, err := parseAppUserModelID(tc.aumid)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseAppUserModelID(%q) = (%q, %q, nil), want error", tc.aumid, packageFamily, applicationID)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAppUserModelID(%q): %v", tc.aumid, err)
			}
			if packageFamily != tc.wantPackageFamily || applicationID != tc.wantApplicationID {
				t.Fatalf("parseAppUserModelID(%q) = (%q, %q), want (%q, %q)", tc.aumid, packageFamily, applicationID, tc.wantPackageFamily, tc.wantApplicationID)
			}
		})
	}
}

func TestDetectedFromPowerShellOutputWrapsOperationalFailure(t *testing.T) {
	runErr := errors.New("powershell failed")
	_, err := detectedFromPowerShellOutput([]byte("access denied"), runErr)
	if !errors.Is(err, runErr) {
		t.Fatalf("err=%v, want run error", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("err=%v, should not be ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("err=%v, want command output", err)
	}
}

func TestDetectedFromPowerShellOutputRejectsMalformedOrInconsistentPayload(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
	}{
		{name: "empty", out: ""},
		{name: "invalid json", out: "not-json"},
		{name: "unknown status", out: `{"status":"maybe","installed":false}`},
		{name: "unknown package", out: `{"status":"ready","installed":true,"package_family_name":"Evil.ChatGPT_deadbeef","install_location":"C:\\Evil","app_user_model_id":"Evil.ChatGPT_deadbeef!App","scheme_registered":true,"scheme_target_valid":true}`},
		{name: "ready without install location", out: `{"status":"ready","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","scheme_registered":true,"scheme_target_valid":true}`},
		{name: "ready without aumid", out: `{"status":"ready","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\ChatGPT","scheme_registered":true,"scheme_target_valid":true}`},
		{name: "ready with mismatched aumid package", out: `{"status":"ready","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\ChatGPT","app_user_model_id":"OpenAI.Codex_2p2nqsd0c76g0!Codex","scheme_registered":true,"scheme_target_valid":true}`},
		{name: "ready with empty aumid application", out: `{"status":"ready","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\ChatGPT","app_user_model_id":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0!","scheme_registered":true,"scheme_target_valid":true}`},
		{name: "ready without scheme", out: `{"status":"ready","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\ChatGPT","app_user_model_id":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0!ChatGPT","scheme_registered":false,"scheme_target_valid":false}`},
		{name: "missing but scheme registered", out: `{"status":"scheme_missing","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\ChatGPT","scheme_registered":true,"scheme_target_valid":false}`},
		{name: "invalid target marked valid", out: `{"status":"scheme_target_invalid","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\ChatGPT","scheme_registered":true,"scheme_target_valid":true}`},
		{name: "non-ready with aumid", out: `{"status":"scheme_target_invalid","installed":true,"package_family_name":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0","install_location":"C:\\ChatGPT","app_user_model_id":"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0!ChatGPT","scheme_registered":true,"scheme_target_valid":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := detectedFromPowerShellOutput([]byte(tc.out), nil)
			if err == nil {
				t.Fatal("expected parsing/validation error")
			}
			for _, sentinel := range []error{ErrNotFound, ErrSchemeMissing, ErrSchemeTargetInvalid} {
				if errors.Is(err, sentinel) {
					t.Fatalf("err=%v should not match availability sentinel %v", err, sentinel)
				}
			}
		})
	}
}

func TestWindowsDetectUsesEffectiveCOMAssociationAcrossBothPackages(t *testing.T) {
	body, err := os.ReadFile("detect_windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"OpenAI.ChatGPT-Desktop_2p2nqsd0c76g0",
		"OpenAI.Codex_2p2nqsd0c76g0",
		"IApplicationAssociationRegistration",
		"QueryCurrentDefault",
		"AssociationType.UrlProtocol",
		"AssociationLevel.Effective",
		"AssocQueryStringW",
		"AssocString.AppUserModelID",
		"Get-InstalledChatGPTCodexPackages",
		"Find-CodexProtocolPackageByAppUserModelID",
		"Get-DiagnosticChatGPTCodexPackage",
		"PackageFamilyName",
		"PackageFullName",
		"InstallLocation",
		"Get-AppxPackageManifest",
		"windows.protocol",
		"AppUserModelID",
		"app_user_model_id",
		"Get-ChatGPTCodexDetection",
		"scheme_missing",
		"scheme_target_invalid",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("detect_windows.ps1 missing secure detection contract %q:\n%s", want, source)
		}
	}
	for _, notWant := range []string{
		"Get-TrustedChatGPTCodexPackage",
		"UserChoice",
		"HKEY_CLASSES_ROOT",
		"AppModel\\Repository",
		"Capabilities\\URLAssociations",
		"Test-TraditionalAssociationTarget",
		"Get-CommandExecutable",
		"$_.Name -like '*Codex*'",
		"$_.Name -like '*ChatGPT*'",
		"$_.PackageFullName -like '*Codex*'",
		"$_.PackageFullName -like '*ChatGPT*'",
		"Invoke-Expression",
		"Start-Process $command",
		"& $command",
		"reg.exe",
	} {
		if strings.Contains(source, notWant) {
			t.Fatalf("detect_windows.ps1 contains unsafe construct %q:\n%s", notWant, source)
		}
	}
	mapping := strings.Index(source, "$mapping = Find-CodexProtocolPackageByAppUserModelID")
	diagnostic := strings.Index(source, "$diagnosticPackage = Get-DiagnosticChatGPTCodexPackage")
	readyAUMID := strings.Index(source, "-AppUserModelID ([string]$mapping.AppUserModelID)")
	if mapping < 0 || diagnostic < 0 || readyAUMID < 0 || diagnostic < mapping || readyAUMID < mapping {
		t.Fatalf("ready selection must come from the effective ProgID mapping before preference-only diagnostics:\n%s", source)
	}
}

func TestWindowsDetectUsesFixedProgIDAssocQueryFlag(t *testing.T) {
	body, err := os.ReadFile("detect_windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"ASSOCF_INIT_FIXED_PROGID",
		"0x0800",
		"AssocQueryStringW(\n                ASSOCF_INIT_FIXED_PROGID,",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("detect_windows.ps1 must pass named ASSOCF_INIT_FIXED_PROGID (0x0800) to AssocQueryStringW; missing %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "AssocQueryStringW(\n                0,") {
		t.Fatalf("detect_windows.ps1 must not pass zero flags to AssocQueryStringW for an already-effective ProgID:\n%s", source)
	}
}

func TestWindowsDetectTreatsFixedProgIDAppIDLookupFailureAsInvalidTarget(t *testing.T) {
	body, err := os.ReadFile("detect_windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	start := strings.Index(source, "function Get-ChatGPTCodexDetection")
	if start < 0 {
		t.Fatalf("detect_windows.ps1 missing Get-ChatGPTCodexDetection:\n%s", source)
	}
	detection := source[start:]
	getProgID := strings.Index(detection, "$effectiveProgId = Get-EffectiveCodexProgId")
	if getProgID < 0 {
		t.Fatalf("Get-ChatGPTCodexDetection must still call Get-EffectiveCodexProgId outside the fixed-ProgID AUMID fallback path:\n%s", detection)
	}
	lookup := strings.Index(detection, "Get-EffectiveAssociationAppUserModelId")
	if lookup < 0 {
		t.Fatalf("Get-ChatGPTCodexDetection must call Get-EffectiveAssociationAppUserModelId:\n%s", detection)
	}
	firstTry := strings.Index(detection, "try {")
	if firstTry < 0 {
		t.Fatalf("fixed-ProgID AUMID lookup must be caught so non-NoAssociation AssocQueryString failures become scheme_target_invalid:\n%s", detection)
	}
	if firstTry < getProgID {
		t.Fatalf("Get-EffectiveCodexProgId/QueryCurrentDefault failures must remain operational, not be caught by the AUMID fallback:\n%s", detection)
	}
	tryBeforeLookup := strings.LastIndex(detection[:lookup], "try {")
	if tryBeforeLookup < 0 {
		t.Fatalf("Get-EffectiveAssociationAppUserModelId must be inside a try block:\n%s", detection)
	}
	emptyInit := strings.LastIndex(detection[:lookup], "$effectiveAppUserModelID = ''")
	if emptyInit < 0 || emptyInit < getProgID || emptyInit > tryBeforeLookup {
		t.Fatalf("installed package + effective scheme must initialize the AUMID to empty before the caught fixed-ProgID lookup:\n%s", detection)
	}
	afterLookup := detection[lookup:]
	catchAfterLookup := strings.Index(afterLookup, "catch {")
	guardAfterLookup := strings.Index(afterLookup, "if (-not [string]::IsNullOrWhiteSpace($effectiveAppUserModelID))")
	if catchAfterLookup < 0 || guardAfterLookup < 0 || catchAfterLookup > guardAfterLookup {
		t.Fatalf("fixed-ProgID AUMID lookup failure must be caught before mapping is considered:\n%s", detection)
	}
	catchBlock := afterLookup[catchAfterLookup:guardAfterLookup]
	if !strings.Contains(catchBlock, "$effectiveAppUserModelID = ''") {
		t.Fatalf("fixed-ProgID AUMID lookup catch must leave AUMID empty so scheme_target_invalid branch runs:\n%s", detection)
	}
	invalidTarget := strings.Index(detection, "return New-ChatGPTCodexDetection -Status 'scheme_target_invalid'")
	if invalidTarget < 0 || invalidTarget < lookup {
		t.Fatalf("installed + effective scheme with empty/failed AUMID must fall through to scheme_target_invalid:\n%s", detection)
	}
}

func TestWindowsDetectEmbedsSharedPowerShellWithoutPolicyBypass(t *testing.T) {
	body, err := os.ReadFile("detect_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, want := range []string{
		"//go:embed detect_windows.ps1",
		"Get-ChatGPTCodexDetection | ConvertTo-Json -Compress",
		`"-NoProfile"`,
		`"-NonInteractive"`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("detect_windows.go missing %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "ExecutionPolicy") || strings.Contains(source, "__codex_desktop_not_found__") {
		t.Fatalf("detect_windows.go must use JSON without policy bypass/sentinel:\n%s", source)
	}
}
