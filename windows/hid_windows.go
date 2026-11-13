package main

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	hidDLL               = windows.NewLazySystemDLL("hid.dll")
	setupDLL             = windows.NewLazySystemDLL("setupapi.dll")
	hidGetGuid           = hidDLL.NewProc("HidD_GetHidGuid")
	hidGetPreparsedData  = hidDLL.NewProc("HidD_GetPreparsedData")
	hidFreePreparsedData = hidDLL.NewProc("HidD_FreePreparsedData")
	hidGetCaps           = hidDLL.NewProc("HidP_GetCaps")
	setupEnumInterfaces  = setupDLL.NewProc("SetupDiEnumDeviceInterfaces")
	setupInterfaceDetail = setupDLL.NewProc("SetupDiGetDeviceInterfaceDetailW")
)

type deviceInterfaceData struct {
	size     uint32
	class    windows.GUID
	flags    uint32
	reserved uintptr
}

func isRawHID(handle windows.Handle) bool {
	var preparsed uintptr
	ok, _, _ := hidGetPreparsedData.Call(uintptr(handle), uintptr(unsafe.Pointer(&preparsed)))
	if ok == 0 {
		return false
	}
	defer hidFreePreparsedData.Call(preparsed)

	var caps struct {
		usage, usagePage, inputBytes, outputBytes, featureBytes uint16
		reserved                                                [27]uint16
	}
	status, _, _ := hidGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps)))
	return uint32(status) == 0x00110000 && caps.usage == 0x61 && caps.usagePage == 0xFF60 && caps.outputBytes == 33
}

func openMeowpad() (windows.Handle, error) {
	var class windows.GUID
	hidGetGuid.Call(uintptr(unsafe.Pointer(&class)))
	devices, err := windows.SetupDiGetClassDevsEx(&class, "", 0, windows.DIGCF_PRESENT|windows.DIGCF_DEVICEINTERFACE, 0, "")
	if err != nil {
		return 0, err
	}
	defer devices.Close()

	for index := uintptr(0); ; index++ {
		var item deviceInterfaceData
		item.size = uint32(unsafe.Sizeof(item))
		ok, _, enumErr := setupEnumInterfaces.Call(uintptr(devices), 0, uintptr(unsafe.Pointer(&class)), index, uintptr(unsafe.Pointer(&item)))
		if ok == 0 {
			if enumErr == windows.ERROR_NO_MORE_ITEMS {
				return 0, errors.New("M4CR0Pad Raw HID interface not found")
			}
			return 0, enumErr
		}

		var bytesRequired uint32
		setupInterfaceDetail.Call(uintptr(devices), uintptr(unsafe.Pointer(&item)), 0, 0, uintptr(unsafe.Pointer(&bytesRequired)), 0)
		if bytesRequired < 8 || bytesRequired > 65536 {
			continue
		}
		detail := make([]uint32, (bytesRequired+3)/4)
		detail[0] = 8 // SP_DEVICE_INTERFACE_DETAIL_DATA_W on Windows x64 and ARM64.
		ok, _, _ = setupInterfaceDetail.Call(uintptr(devices), uintptr(unsafe.Pointer(&item)), uintptr(unsafe.Pointer(&detail[0])), uintptr(bytesRequired), 0, 0)
		if ok == 0 {
			continue
		}
		path := unsafe.Slice((*uint16)(unsafe.Pointer(&detail[1])), int((bytesRequired-4)/2))
		if !strings.Contains(strings.ToLower(windows.UTF16ToString(path)), "vid_3393&pid_0138") {
			continue
		}
		handle, err := windows.CreateFile(&path[0], windows.GENERIC_WRITE|windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			continue
		}
		if isRawHID(handle) {
			return handle, nil
		}
		windows.CloseHandle(handle)
	}
}
