package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
	"golang.org/x/sys/windows"
)

var (
	// IPolicyConfig is the Windows desktop COM interface used to set the default endpoint.
	policyClass      = ole.NewGUID("870AF99C-171D-4F9E-AF0D-E63DF40C2BC9")
	policyInterface  = ole.NewGUID("F8679F50-850A-41CF-9C72-430F290290C8")
	propVariantClear = windows.NewLazySystemDLL("ole32.dll").NewProc("PropVariantClear")
)

type audioOutput struct {
	id   string
	name string
}

func defaultOutput(enumerator *wca.IMMDeviceEnumerator, role uint32) (string, error) {
	var device *wca.IMMDevice
	if err := enumerator.GetDefaultAudioEndpoint(wca.ERender, role, &device); err != nil {
		return "", err
	}
	defer device.Release()
	return outputID(device)
}

func outputID(device *wca.IMMDevice) (string, error) {
	var id *uint16
	hr, _, _ := syscall.SyscallN(device.VTable().GetId, uintptr(unsafe.Pointer(device)), uintptr(unsafe.Pointer(&id)))
	if int32(hr) < 0 {
		return "", ole.NewError(hr)
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(id)))
	return windows.UTF16PtrToString(id), nil
}

func outputName(device *wca.IMMDevice) string {
	var store *wca.IPropertyStore
	if err := device.OpenPropertyStore(wca.STGM_READ, &store); err != nil {
		return ""
	}
	defer store.Release()

	// A PROPVARIANT occupies 24 bytes on the supported 64-bit targets.
	var value struct {
		kind uint16
		_    [3]uint16
		text *uint16
		_    uintptr
	}
	hr, _, _ := syscall.SyscallN(store.VTable().GetValue, uintptr(unsafe.Pointer(store)),
		uintptr(unsafe.Pointer(&wca.PKEY_Device_FriendlyName)), uintptr(unsafe.Pointer(&value)))
	defer func() { propVariantClear.Call(uintptr(unsafe.Pointer(&value))) }()
	if int32(hr) < 0 || value.kind != uint16(ole.VT_LPWSTR) {
		return ""
	}
	return windows.UTF16PtrToString(value.text)
}

func audioOutputs(enumerator *wca.IMMDeviceEnumerator) ([]audioOutput, error) {
	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ERender, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, err
	}
	defer collection.Release()

	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, err
	}
	outputs := make([]audioOutput, 0, count)
	for index := uint32(0); index < count; index++ {
		var device *wca.IMMDevice
		if err := collection.Item(index, &device); err != nil {
			continue
		}
		id, err := outputID(device)
		name := outputName(device)
		device.Release()
		if err != nil || id == "" {
			continue
		}
		if name == "" {
			name = fmt.Sprintf("Аудиовыход %d", index+1)
		}
		outputs = append(outputs, audioOutput{id: id, name: name})
	}
	sort.SliceStable(outputs, func(i, j int) bool {
		return strings.ToLower(outputs[i].name) < strings.ToLower(outputs[j].name)
	})
	return outputs, nil
}

func switchOutput(enumerator *wca.IMMDeviceEnumerator, id string) error {
	var policy *ole.IUnknown
	if err := wca.CoCreateInstance(policyClass, 0, ole.CLSCTX_ALL, policyInterface, &policy); err != nil {
		return err
	}
	defer policy.Release()

	previous, err := defaultOutput(enumerator, wca.EConsole)
	if err != nil {
		return err
	}
	if err := setDefaultEndpoint(policy, id, wca.EConsole); err != nil {
		return err
	}
	if err := setDefaultEndpoint(policy, id, wca.EMultimedia); err != nil {
		if rollbackErr := setDefaultEndpoint(policy, previous, wca.EConsole); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("Не удалось вернуть прежний выход: %w", rollbackErr))
		}
		return err
	}
	return nil
}

func setDefaultEndpoint(policy *ole.IUnknown, id string, role uint32) error {
	// Three IUnknown methods and ten IPolicyConfig methods precede SetDefaultEndpoint.
	vtable := (*[14]uintptr)(unsafe.Pointer(policy.VTable()))
	hr, _, _ := syscall.SyscallN(vtable[13], uintptr(unsafe.Pointer(policy)),
		uintptr(unsafe.Pointer(utf16Text(id))), uintptr(role))
	if int32(hr) < 0 {
		return ole.NewError(hr)
	}
	return nil
}
