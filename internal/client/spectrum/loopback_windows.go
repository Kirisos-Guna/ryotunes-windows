//go:build windows

package spectrum

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The visualizer's audio on Windows: a WASAPI loopback capture of the
// default output device, which hears everything Ryotunes plays (mpv and the
// Spotify Web Player alike). It stands in for cava and pw-record on Linux.

const loopbackSource = "wasapi-loopback"

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")

	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioClient         = windows.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4C32, Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidIAudioCaptureClient  = windows.GUID{Data1: 0xC8ADBD64, Data2: 0xE71E, Data3: 0x48A0, Data4: [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
	subtypeIEEEFloat        = windows.GUID{Data1: 0x00000003, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
)

const (
	clsctxAll            = 0x17
	eRender              = 0
	eConsole             = 0
	sharedMode           = 0
	streamFlagsLoopback  = 0x00020000
	bufferFlagsSilent    = 0x2
	formatTagFloat       = 3
	formatTagPCM         = 1
	formatTagExtensible  = 0xFFFE
	captureBuffer100ns   = 200 * 10000 // 200 ms
	vtGetDefaultEndpoint = 4
	vtActivate           = 3
	vtClientInitialize   = 3
	vtClientGetMixFormat = 8
	vtClientStart        = 10
	vtClientStop         = 11
	vtClientGetService   = 14
	vtCaptureGetBuffer   = 3
	vtCaptureReleaseBuf  = 4
	vtCaptureNextPacket  = 5
	vtRelease            = 2
)

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type waveFormatExtensible struct {
	waveFormatEx
	ValidBits   uint16
	ChannelMask uint32
	SubFormat   windows.GUID
}

// comCall calls method index of a COM object's vtable.
func comCall(object unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtable := *(*unsafe.Pointer)(object)
	method := *(*uintptr)(unsafe.Add(vtable, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	result, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(object)}, args...)...)
	return result
}

func hresult(name string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s: HRESULT %#x", name, uint32(hr))
	}
	return nil
}

func loopbackAvailable() bool { return true }

// startLoopback captures the default output device and hands each packet,
// as interleaved stereo float32 at the device's rate, to onSamples.
func startLoopback(onSamples func(stereo []float32, rate int)) (*runningCapture, error) {
	ready := make(chan error, 1)
	wait := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		wait <- runLoopback(onSamples, stop, ready)
	}()
	select {
	case err := <-ready:
		if err != nil {
			return nil, err
		}
	case <-time.After(5 * time.Second):
		close(stop)
		return nil, errors.New("audio loopback did not start")
	}
	stopped := false
	return &runningCapture{
		wait: wait,
		stop: func() {
			if !stopped {
				stopped = true
				close(stop)
			}
		},
	}, nil
}

func runLoopback(onSamples func([]float32, int), stop <-chan struct{}, ready chan<- error) (err error) {
	fail := func(e error) error {
		select {
		case ready <- e:
		default:
		}
		return e
	}
	hr, _, _ := procCoInitializeEx.Call(0, 0)      // COINIT_MULTITHREADED
	if int32(hr) < 0 && uint32(hr) != 0x80010106 { // RPC_E_CHANGED_MODE
		return fail(hresult("CoInitializeEx", hr))
	}
	defer procCoUninitialize.Call()

	var enumerator unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enumerator)))
	if e := hresult("CoCreateInstance", hr); e != nil {
		return fail(e)
	}
	defer comCall(enumerator, vtRelease)

	var device unsafe.Pointer
	if e := hresult("GetDefaultAudioEndpoint", comCall(enumerator, vtGetDefaultEndpoint, eRender, eConsole, uintptr(unsafe.Pointer(&device)))); e != nil {
		return fail(e)
	}
	defer comCall(device, vtRelease)

	var client unsafe.Pointer
	if e := hresult("Activate", comCall(device, vtActivate, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&client)))); e != nil {
		return fail(e)
	}
	defer comCall(client, vtRelease)

	var format *waveFormatEx
	if e := hresult("GetMixFormat", comCall(client, vtClientGetMixFormat, uintptr(unsafe.Pointer(&format)))); e != nil {
		return fail(e)
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(format)))

	isFloat := format.FormatTag == formatTagFloat
	if format.FormatTag == formatTagExtensible {
		isFloat = (*waveFormatExtensible)(unsafe.Pointer(format)).SubFormat == subtypeIEEEFloat
	}
	channels := int(format.Channels)
	bits := int(format.BitsPerSample)
	rate := int(format.SamplesPerSec)
	if channels < 1 || (isFloat && bits != 32) || (!isFloat && bits != 16 && bits != 32) {
		return fail(fmt.Errorf("unsupported mix format: %d channels, %d bits", channels, bits))
	}

	if e := hresult("Initialize", comCall(client, vtClientInitialize, sharedMode, streamFlagsLoopback,
		captureBuffer100ns, 0, uintptr(unsafe.Pointer(format)), 0)); e != nil {
		return fail(e)
	}
	var capture unsafe.Pointer
	if e := hresult("GetService", comCall(client, vtClientGetService, uintptr(unsafe.Pointer(&iidIAudioCaptureClient)), uintptr(unsafe.Pointer(&capture)))); e != nil {
		return fail(e)
	}
	defer comCall(capture, vtRelease)
	if e := hresult("Start", comCall(client, vtClientStart)); e != nil {
		return fail(e)
	}
	defer comCall(client, vtClientStop)
	ready <- nil

	frameBytes := channels * bits / 8
	var stereo []float32
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
		}
		for {
			var packet uint32
			if e := hresult("GetNextPacketSize", comCall(capture, vtCaptureNextPacket, uintptr(unsafe.Pointer(&packet)))); e != nil {
				return e // the device changed or went away: the lifecycle restarts it
			}
			if packet == 0 {
				break
			}
			var data *byte
			var frames, flags uint32
			if e := hresult("GetBuffer", comCall(capture, vtCaptureGetBuffer, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0)); e != nil {
				return e
			}
			n := int(frames)
			if cap(stereo) < n*2 {
				stereo = make([]float32, n*2)
			}
			stereo = stereo[:n*2]
			if flags&bufferFlagsSilent != 0 || data == nil {
				clear(stereo)
			} else {
				raw := unsafe.Slice(data, n*frameBytes)
				for frame := 0; frame < n; frame++ {
					left := sampleAt(raw, frame*frameBytes, bits, isFloat)
					right := left
					if channels > 1 {
						right = sampleAt(raw, frame*frameBytes+bits/8, bits, isFloat)
					}
					stereo[frame*2], stereo[frame*2+1] = left, right
				}
			}
			comCall(capture, vtCaptureReleaseBuf, uintptr(frames))
			onSamples(stereo, rate)
		}
	}
}

func sampleAt(raw []byte, offset, bits int, isFloat bool) float32 {
	switch {
	case isFloat:
		return math.Float32frombits(uint32(raw[offset]) | uint32(raw[offset+1])<<8 | uint32(raw[offset+2])<<16 | uint32(raw[offset+3])<<24)
	case bits == 16:
		return float32(int16(uint16(raw[offset])|uint16(raw[offset+1])<<8)) / 32768
	default:
		return float32(int32(uint32(raw[offset])|uint32(raw[offset+1])<<8|uint32(raw[offset+2])<<16|uint32(raw[offset+3])<<24)) / 2147483648
	}
}
