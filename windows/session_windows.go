package main

import (
	"errors"
	"math"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

var (
	getForegroundWindow = user32.NewProc("GetForegroundWindow")
	getWindowProcessID  = user32.NewProc("GetWindowThreadProcessId")
)

func foregroundExecutable() string {
	window, _, _ := getForegroundWindow.Call()
	var pid uint32
	getWindowProcessID.Call(window, uintptr(unsafe.Pointer(&pid)))
	return processExecutable(pid)
}

func processExecutable(pid uint32) string {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	var buffer [1024]uint16
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return ""
	}
	return strings.ToLower(filepath.Base(windows.UTF16ToString(buffer[:size])))
}

func changeAppVolume(enumerator *wca.IMMDeviceEnumerator, executable string, step float32) (int, error) {
	if executable == "" {
		return 0, errors.New("не удалось определить активное приложение")
	}
	var device *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &device); err != nil {
		return 0, err
	}
	defer device.Release()
	var manager *wca.IAudioSessionManager2
	if err := device.Activate(wca.IID_IAudioSessionManager2, ole.CLSCTX_ALL, nil, &manager); err != nil {
		return 0, err
	}
	defer manager.Release()
	var sessions *wca.IAudioSessionEnumerator
	if err := manager.GetSessionEnumerator(&sessions); err != nil {
		return 0, err
	}
	defer sessions.Release()
	var count int
	if err := sessions.GetCount(&count); err != nil {
		return 0, err
	}
	var level int
	var changed bool
	for index := 0; index < count; index++ {
		var session *wca.IAudioSessionControl
		if err := sessions.GetSession(index, &session); err != nil {
			continue
		}
		percent, ok := adjustSession(session, executable, step)
		session.Release()
		if ok && !changed {
			level, changed = percent, true
		}
	}
	if !changed {
		return 0, errors.New("у приложения нет доступной аудиосессии на основном выходе")
	}
	return level, nil
}

func adjustSession(session *wca.IAudioSessionControl, executable string, step float32) (int, bool) {
	var control *wca.IAudioSessionControl2
	if err := session.PutQueryInterface(wca.IID_IAudioSessionControl2, &control); err != nil {
		return 0, false
	}
	defer control.Release()
	var pid, state uint32
	if control.GetProcessId(&pid) != nil || session.GetState(&state) != nil || state == 2 || processExecutable(pid) != executable {
		return 0, false
	}
	var volume *wca.ISimpleAudioVolume
	if err := session.PutQueryInterface(wca.IID_ISimpleAudioVolume, &volume); err != nil {
		return 0, false
	}
	defer volume.Release()
	var level float32
	if volume.GetMasterVolume(&level) != nil {
		return 0, false
	}
	level = max(0, min(1, level+step))
	if err := setSessionVolume(volume, level); err != nil {
		return 0, false
	}
	return int(math.Round(float64(level) * 100)), true
}

func setSessionVolume(volume *wca.ISimpleAudioVolume, level float32) error {
	// SyscallN mirrors integer arguments into floating-point registers on Windows x64.
	if err := volume.SetMasterVolume(level, nil); err != nil {
		return err
	}
	var actual float32
	if err := volume.GetMasterVolume(&actual); err != nil {
		return err
	}
	if math.Abs(float64(actual-level)) > 0.001 {
		return errors.New("Windows не применил громкость аудиосессии")
	}
	return nil
}
