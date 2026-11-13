package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

func readVolume(enumerator *wca.IMMDeviceEnumerator) ([33]byte, error) {
	var device *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &device); err != nil {
		return [33]byte{}, err
	}
	defer device.Release()

	var endpoint *wca.IAudioEndpointVolume
	if err := device.Activate(wca.IID_IAudioEndpointVolume, ole.CLSCTX_ALL, nil, &endpoint); err != nil {
		return [33]byte{}, err
	}
	defer endpoint.Release()

	var scalar float32
	if err := endpoint.GetMasterVolumeLevelScalar(&scalar); err != nil {
		return [33]byte{}, err
	}
	var muted uint32 // Core Audio returns a four-byte Windows BOOL.
	hr, _, _ := syscall.SyscallN(endpoint.VTable().GetMute, uintptr(unsafe.Pointer(endpoint)), uintptr(unsafe.Pointer(&muted)))
	if uint32(hr) != 0 {
		return [33]byte{}, ole.NewError(hr)
	}
	return volumeReport(scalar, muted != 0), nil
}

func audioEnumerator() (*wca.IMMDeviceEnumerator, func(), error) {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if oleErr, ok := err.(*ole.OleError); !ok || oleErr.Code() != 1 { // S_FALSE: already initialized.
			return nil, nil, err
		}
	}

	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, ole.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		ole.CoUninitialize()
		return nil, nil, err
	}
	return enumerator, func() {
		enumerator.Release()
		ole.CoUninitialize()
	}, nil
}

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := run(); err != nil {
		showError(err)
	}
}

func run() error {
	mutex, err := windows.CreateMutex(nil, false, utf16Text(`Local\Meowpad-Volume`))
	if mutex != 0 {
		defer windows.CloseHandle(mutex)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	if err != nil {
		return err
	}
	enumerator, cleanup, err := audioEnumerator()
	if err != nil {
		return fmt.Errorf("Не удалось подключиться к аудиосистеме Windows: %w", err)
	}
	defer cleanup()

	stop := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	media := make(chan mediaState, 1)
	overlays := make(chan [33]byte, 1)
	mediaDone := make(chan struct{})
	go func() {
		defer close(mediaDone)
		watchMedia(ctx, media)
	}()
	go syncVolume(stop, media, overlays)
	defer func() {
		cancel()
		close(stop)
		<-mediaDone
	}()
	return runTray(enumerator, overlays)
}

func syncVolume(stop <-chan struct{}, media <-chan mediaState, overlays <-chan [33]byte) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	enumerator, cleanup, err := audioEnumerator()
	if err != nil {
		return
	}
	defer cleanup()
	tick := time.NewTicker(125 * time.Millisecond)
	defer tick.Stop()

	var device windows.Handle
	defer func() {
		if device != 0 {
			windows.CloseHandle(device)
		}
	}()
	var lastReport [33]byte
	var lastSend, nextConnect time.Time
	var track mediaState
	var lastTrack [33]byte
	var lastTrackSend time.Time
	for {
		var now time.Time
		select {
		case <-stop:
			return
		case track = <-media:
			continue
		case overlay := <-overlays:
			if device != 0 && !writeReport(device, overlay) {
				windows.CloseHandle(device)
				device = 0
			}
			continue
		case now = <-tick.C:
		}
		if device == 0 {
			if now.Before(nextConnect) {
				continue
			}
			nextConnect = now.Add(time.Second)
			var err error
			device, err = openMeowpad()
			if err != nil {
				continue
			}
			lastSend = time.Time{}
			lastTrackSend = time.Time{}
		}
		report, err := readVolume(enumerator)
		if err == nil && (report != lastReport || now.Sub(lastSend) >= 2*time.Second) {
			if !writeReport(device, report) {
				windows.CloseHandle(device)
				device = 0
				continue
			}
			lastReport, lastSend = report, now
		}
		song := trackReport(track)
		if song == lastTrack && now.Sub(lastTrackSend) < 2*time.Second {
			continue
		}
		if !writeReport(device, song) {
			windows.CloseHandle(device)
			device = 0
			continue
		}
		lastTrack, lastTrackSend = song, now
	}
}

func writeReport(device windows.Handle, report [33]byte) bool {
	var written uint32
	err := windows.WriteFile(device, report[:], &written, nil)
	return err == nil && written == uint32(len(report))
}
