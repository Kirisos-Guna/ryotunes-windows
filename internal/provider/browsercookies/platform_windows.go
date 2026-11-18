//go:build windows

package browsercookies

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// platformRoots lists the Windows browser profile roots. Each root sits a
// few levels above the cookie store so Discover's depth limit still applies:
//
//	Firefox   %APPDATA%\Mozilla\Firefox\Profiles\<profile>\cookies.sqlite
//	Chromium  %LOCALAPPDATA%\<vendor>\User Data\<profile>\Network\Cookies
func platformRoots() ([]string, bool) {
	roaming := os.Getenv("APPDATA")
	local := os.Getenv("LOCALAPPDATA")
	var roots []string
	add := func(base string, parts ...string) {
		if base == "" {
			return
		}
		path := filepath.Join(append([]string{base}, parts...)...)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			roots = append(roots, filepath.Clean(path))
		}
	}
	add(roaming, "Mozilla", "Firefox")
	add(roaming, "librewolf")
	add(roaming, "Waterfox")
	add(roaming, "zen")
	add(roaming, "Floorp")
	add(local, "Google", "Chrome", "User Data")
	add(local, "Chromium", "User Data")
	add(local, "Microsoft", "Edge", "User Data")
	add(local, "BraveSoftware", "Brave-Browser", "User Data")
	add(local, "Vivaldi", "User Data")
	add(roaming, "Opera Software", "Opera Stable")
	add(roaming, "Opera Software", "Opera GX Stable")
	return roots, true
}

// platformDecrypt decrypts a Windows Chromium cookie. "v10" values are
// AES-256-GCM under a key stored DPAPI-protected in "Local State". Chrome's
// app-bound "v20" values are tied to the browser itself and cannot be read
// by other programs; those cookies are skipped.
func platformDecrypt(st Store, encrypted []byte, host string) (string, bool) {
	switch string(encrypted[:3]) {
	case "v10", "v11":
	default:
		return "", true
	}
	key, err := localStateKey(st.Path)
	if err != nil {
		return "", true
	}
	value, err := decryptGCM(encrypted[3:], key, host)
	if err != nil {
		return "", true
	}
	return value, true
}

var (
	keyCacheMu sync.Mutex
	keyCache   = map[string][]byte{}
)

// localStateKey finds "Local State" above a Cookies file and unwraps its key.
func localStateKey(cookiesPath string) ([]byte, error) {
	dir := filepath.Dir(cookiesPath)
	for i := 0; i < 4; i++ {
		statePath := filepath.Join(dir, "Local State")
		if _, err := os.Stat(statePath); err == nil {
			keyCacheMu.Lock()
			defer keyCacheMu.Unlock()
			if key, ok := keyCache[statePath]; ok {
				return key, nil
			}
			key, err := readLocalStateKey(statePath)
			if err != nil {
				return nil, err
			}
			keyCache[statePath] = key
			return key, nil
		}
		dir = filepath.Dir(dir)
	}
	return nil, errors.New("Chromium Local State not found")
}

func readLocalStateKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(raw), "DPAPI") {
		return nil, errors.New("unexpected Chromium key format")
	}
	return dpapiDecrypt(raw[len("DPAPI"):])
}

func dpapiDecrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty DPAPI blob")
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func decryptGCM(payload, key []byte, host string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize()+gcm.Overhead() {
		return "", errors.New("short Chromium cookie ciphertext")
	}
	nonce, sealed := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", err
	}
	// Chromium 130+ prefixes the value with SHA-256 of the host key.
	digest := sha256.Sum256([]byte(strings.TrimPrefix(host, ".")))
	if len(plain) >= len(digest) && string(plain[:len(digest)]) == string(digest[:]) {
		plain = plain[len(digest):]
	}
	return string(plain), nil
}

// platformDefaultBrowser maps the https handler's ProgId to tokens that
// storePriority can match against profile paths.
func platformDefaultBrowser() (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`, registry.QUERY_VALUE)
	if err != nil {
		return "", true
	}
	defer key.Close()
	progID, _, err := key.GetStringValue("ProgId")
	if err != nil {
		return "", true
	}
	id := strings.ToLower(progID)
	switch {
	case strings.HasPrefix(id, "firefox"):
		return "firefox", true
	case strings.HasPrefix(id, "librewolf"):
		return "librewolf", true
	case strings.HasPrefix(id, "waterfox"):
		return "waterfox", true
	case strings.HasPrefix(id, "zen"):
		return "zen", true
	case strings.HasPrefix(id, "chromehtml"):
		return "google-chrome", true
	case strings.HasPrefix(id, "msedge"):
		return "microsoft-edge", true
	case strings.HasPrefix(id, "brave"):
		return "brave", true
	case strings.HasPrefix(id, "vivaldi"):
		return "vivaldi", true
	case strings.HasPrefix(id, "opera"):
		return "opera", true
	case strings.HasPrefix(id, "chromium"):
		return "chromium", true
	}
	return "", true
}
