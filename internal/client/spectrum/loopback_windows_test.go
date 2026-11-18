//go:build windows

package spectrum

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestLoopbackLive captures the default output for two seconds; play music
// first. Set RYOTUNES_AUDIO_TESTS=1 to run it.
func TestLoopbackLive(t *testing.T) {
	if os.Getenv("RYOTUNES_AUDIO_TESTS") == "" {
		t.Skip("set RYOTUNES_AUDIO_TESTS=1 with music playing")
	}
	analyzer := newAnalyzer()
	var mu sync.Mutex
	var frames, packets int
	var peak float32
	var last [Bands]float32
	capture, err := startLoopback(func(stereo []float32, rate int) {
		mu.Lock()
		defer mu.Unlock()
		packets++
		for _, v := range stereo {
			peak = max(peak, v, -v)
		}
		if levels, ok := analyzer.push(stereo, rate, time.Now()); ok {
			frames++
			last = levels
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	capture.stop()
	<-capture.wait
	mu.Lock()
	defer mu.Unlock()
	const shades = " .:-=+*#%@"
	var bar strings.Builder
	for _, level := range last {
		bar.WriteByte(shades[min(9, int(level*10))])
	}
	t.Logf("packets %d, spectrum frames %d, peak sample %.3f", packets, frames, peak)
	t.Logf("bars |%s|", bar.String())
	if packets == 0 {
		t.Fatal("no audio packets: is something playing?")
	}
}

// TestLoopbackDevices measures the sound on every active output device and
// which one is the default for each role. Set RYOTUNES_AUDIO_TESTS=1.
func TestLoopbackDevices(t *testing.T) {
	if os.Getenv("RYOTUNES_AUDIO_TESTS") == "" {
		t.Skip("set RYOTUNES_AUDIO_TESTS=1 with music playing")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, 0)
	var enumerator unsafe.Pointer
	procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enumerator)))
	idOf := func(device unsafe.Pointer) string {
		var id *uint16
		comCall(device, 5, uintptr(unsafe.Pointer(&id)))
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(id)))
		return windows.UTF16PtrToString(id)
	}
	for role, name := range []string{"console", "multimedia", "communications"} {
		var device unsafe.Pointer
		comCall(enumerator, vtGetDefaultEndpoint, eRender, uintptr(role), uintptr(unsafe.Pointer(&device)))
		if device != nil {
			t.Logf("default %-14s %s", name, idOf(device))
			comCall(device, vtRelease)
		}
	}
	var collection unsafe.Pointer
	comCall(enumerator, 3, eRender, 1, uintptr(unsafe.Pointer(&collection))) // DEVICE_STATE_ACTIVE
	var count uint32
	comCall(collection, 3, uintptr(unsafe.Pointer(&count)))
	for i := uint32(0); i < count; i++ {
		var device unsafe.Pointer
		comCall(collection, 4, uintptr(i), uintptr(unsafe.Pointer(&device)))
		id := idOf(device)
		var peak float32
		var mu sync.Mutex
		stop := make(chan struct{})
		ready := make(chan error, 1)
		done := make(chan error, 1)
		go func() {
			runtime.LockOSThread()
			done <- runLoopbackOn(func(unsafe.Pointer) (unsafe.Pointer, error) { comCall(device, 1); return device, nil },
				func(stereo []float32, _ int) {
					mu.Lock()
					for _, v := range stereo {
						peak = max(peak, v, -v)
					}
					mu.Unlock()
				}, stop, ready)
		}()
		err := <-ready
		time.Sleep(1500 * time.Millisecond)
		close(stop)
		<-done
		mu.Lock()
		t.Logf("device %d %s: peak %.3f err %v", i, id, peak, err)
		mu.Unlock()
	}
}

// TestAudioSessions lists the default output's audio sessions: process,
// mixer volume, mute and current peak. Set RYOTUNES_AUDIO_TESTS=1.
func TestAudioSessions(t *testing.T) {
	if os.Getenv("RYOTUNES_AUDIO_TESTS") == "" {
		t.Skip("set RYOTUNES_AUDIO_TESTS=1")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procCoInitializeEx.Call(0, 0)
	guid := func(d1 uint32, d2, d3 uint16, d4 [8]byte) windows.GUID {
		return windows.GUID{Data1: d1, Data2: d2, Data3: d3, Data4: d4}
	}
	iidManager2 := guid(0x77AA99A0, 0x1BD6, 0x484F, [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F})
	iidControl2 := guid(0xbfb7ff88, 0x7239, 0x4fc9, [8]byte{0x8f, 0xa2, 0x07, 0xc9, 0x50, 0xbe, 0x9c, 0x6d})
	iidVolume := guid(0x87CE5498, 0x68D6, 0x44E5, [8]byte{0x92, 0x15, 0x6D, 0xA4, 0x7E, 0xF8, 0x83, 0xD8})
	iidMeter := guid(0xC02216F6, 0x8C67, 0x4B5B, [8]byte{0x9D, 0x00, 0xD0, 0x08, 0xE7, 0x3E, 0x00, 0x64})
	var enumerator, device, manager, sessions unsafe.Pointer
	procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enumerator)))
	comCall(enumerator, vtGetDefaultEndpoint, eRender, eConsole, uintptr(unsafe.Pointer(&device)))
	comCall(device, vtActivate, uintptr(unsafe.Pointer(&iidManager2)), clsctxAll, 0, uintptr(unsafe.Pointer(&manager)))
	comCall(manager, 5, uintptr(unsafe.Pointer(&sessions)))
	var count int32
	comCall(sessions, 3, uintptr(unsafe.Pointer(&count)))
	for i := int32(0); i < count; i++ {
		var control, control2, volume, meter unsafe.Pointer
		comCall(sessions, 4, uintptr(i), uintptr(unsafe.Pointer(&control)))
		comCall(control, 0, uintptr(unsafe.Pointer(&iidControl2)), uintptr(unsafe.Pointer(&control2)))
		comCall(control, 0, uintptr(unsafe.Pointer(&iidVolume)), uintptr(unsafe.Pointer(&volume)))
		comCall(control, 0, uintptr(unsafe.Pointer(&iidMeter)), uintptr(unsafe.Pointer(&meter)))
		var pid, state uint32
		var level, peak float32
		var muted int32
		comCall(control2, 14, uintptr(unsafe.Pointer(&pid)))
		comCall(control, 3, uintptr(unsafe.Pointer(&state)))
		comCall(volume, 4, uintptr(unsafe.Pointer(&level)))
		comCall(volume, 6, uintptr(unsafe.Pointer(&muted)))
		time.Sleep(200 * time.Millisecond)
		comCall(meter, 3, uintptr(unsafe.Pointer(&peak)))
		name := ""
		if handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
			buf := make([]uint16, 512)
			size := uint32(len(buf))
			if windows.QueryFullProcessImageName(handle, 0, &buf[0], &size) == nil {
				name = windows.UTF16ToString(buf[:size])
			}
			windows.CloseHandle(handle)
		}
		t.Logf("session pid %-6d state %d volume %.2f muted %d peak %.3f %s", pid, state, level, muted, peak, name)
	}
}
