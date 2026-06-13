//go:build windows

package secrets

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	modadvapi32     = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW   = modadvapi32.NewProc("CredReadW")
	procCredWriteW  = modadvapi32.NewProc("CredWriteW")
	procCredDeleteW = modadvapi32.NewProc("CredDeleteW")
	procCredFree    = modadvapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
	errNoSuchLogonSession   = syscall.Errno(1312)
)

// CREDENTIAL mirrors the Windows CREDENTIALW structure (subset).
type winCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        [8]byte // FILETIME
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func credTarget(key string) string {
	return fmt.Sprintf("%s/%s", serviceName, key)
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func keyringAvailable() bool { return true }

type keyringStore struct {
	mu sync.Mutex
}

func newKeyringStore() *keyringStore {
	return &keyringStore{}
}

func (k *keyringStore) Get(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	target := utf16Ptr(credTarget(key))
	var pcred *winCredential
	r, _, err := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&pcred)),
	)
	if r == 0 {
		if isCredentialMissing(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("CredReadW: %w", err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(pcred)))
	if pcred.CredentialBlobSize%2 != 0 {
		return "", fmt.Errorf("CredReadW: odd credential blob size %d", pcred.CredentialBlobSize)
	}
	blob := unsafe.Slice(pcred.CredentialBlob, pcred.CredentialBlobSize)
	u16 := make([]uint16, pcred.CredentialBlobSize/2)
	for i := range u16 {
		u16[i] = uint16(blob[i*2]) | uint16(blob[i*2+1])<<8
	}
	return string(utf16.Decode(u16)), nil
}

func (k *keyringStore) Set(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	u16 := utf16.Encode([]rune(value))
	blob := make([]byte, len(u16)*2)
	for i, v := range u16 {
		blob[i*2] = byte(v)
		blob[i*2+1] = byte(v >> 8)
	}
	target := credTarget(key)
	label := fmt.Sprintf("%s: %s", serviceName, key)
	cred := winCredential{
		Type:               credTypeGeneric,
		TargetName:         utf16Ptr(target),
		Comment:            utf16Ptr(label),
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachine,
		UserName:           utf16Ptr(strings.ToLower(key)),
	}
	r, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r == 0 {
		return fmt.Errorf("CredWriteW: %w", err)
	}
	return nil
}

func (k *keyringStore) Delete(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	target := utf16Ptr(credTarget(key))
	r, _, err := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(target)),
		credTypeGeneric,
		0,
	)
	if r == 0 {
		if isCredentialMissing(err) {
			return nil
		}
		return fmt.Errorf("CredDeleteW: %w", err)
	}
	return nil
}

func isCredentialMissing(err error) bool {
	errno, ok := err.(syscall.Errno)
	if !ok {
		return false
	}
	return errno == syscall.ERROR_NOT_FOUND || errno == errNoSuchLogonSession
}
