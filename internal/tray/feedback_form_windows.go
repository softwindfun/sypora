//go:build windows

package tray

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fbFormW = 480
	fbFormH = 350

	fbMargin   = 20
	fbLblH     = 20
	fbLblGap   = 4
	fbEdtY     = fbMargin + fbLblH + fbLblGap // 44
	fbEdtW     = fbFormW - 2*fbMargin
	fbEdtH     = 200
	fbBtnW     = 90
	fbBtnH     = 28
	fbBtnGap   = 8
	fbBtnY     = fbEdtY + fbEdtH + 16 // 260
)

const (
	esMultiline   = 0x0004
	esWantReturn  = 0x1000
	esAutoVScroll = 0x0040
	wsVScroll     = 0x00200000
)

const (
	fbCtlEdit      = 201
	fbCtlSubmitBtn = 202
	fbCtlCancelBtn = 203
)

var (
	fbClassOnce sync.Once
	fbWndProc   uintptr
	fbClassName *uint16
	fbClassAtom uintptr

	fbText     string
	fbHwnd     syscall.Handle
	fbHinst    syscall.Handle
	fbEditHwnd syscall.Handle
	fbSaved    bool
)

func showFeedbackForm() (string, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fbText = ""
	fbSaved = false

	hinst, _, _ := pGetModuleHandle.Call(0)
	fbHinst = syscall.Handle(hinst)

	fbClassOnce.Do(func() {
		fbClassName = utf16Ptr("SyporaFeedbackForm")
		fbWndProc = syscall.NewCallback(fbWindowProc)

		brush := appBackgroundBrush()
		arrowCursor, _, _ := pLoadCursor.Call(0, uintptr(idcArrow))

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   fbWndProc,
			hInstance:     fbHinst,
			hCursor:       syscall.Handle(arrowCursor),
			hbrBackground: syscall.Handle(brush),
			lpszClassName: fbClassName,
		}
		r, _, _ := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		fbClassAtom = r
	})

	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsVisible | wsDlgFrame)
	exStyle := uintptr(wsExDlgModalFrame | wsExControlParent)

	r, _, _ := pCreateWindowEx.Call(
		exStyle,
		uintptr(unsafe.Pointer(fbClassName)),
		uintptr(unsafe.Pointer(utf16Ptr("用户反馈"))),
		style,
		uintptr(cwUseDefault), uintptr(cwUseDefault),
		uintptr(fbFormW), uintptr(fbFormH),
		0, 0, uintptr(fbHinst), 0,
	)
	if r == 0 {
		return "", false
	}
	fbHwnd = syscall.Handle(r)

	centerWindowOnScreen(fbHwnd)
	applyRoundedCorners(fbHwnd, fbFormW, fbFormH, 8)

	pShowWindow.Call(uintptr(fbHwnd), uintptr(windows.SW_SHOW))
	pUpdateWindow.Call(uintptr(fbHwnd))

	type winMsg struct {
		hwnd    syscall.Handle
		message uint32
		wParam  uintptr
		lParam  uintptr
		time    uint32
		ptX     int32
		ptY     int32
	}
	var msg winMsg
	for {
		ret, _, _ := pGetMessage.Call(
			uintptr(unsafe.Pointer(&msg)),
			0, 0, 0,
		)
		if ret == 0 || int32(ret) == -1 {
			break
		}
		dlg, _, _ := pIsDialogMessage.Call(uintptr(fbHwnd), uintptr(unsafe.Pointer(&msg)))
		if dlg == 0 {
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			pDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}

	if fbSaved {
		return fbText, true
	}
	return "", false
}

func fbWindowProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCreate:
		return fbOnCreate(hwnd)
	case wmCommand:
		return fbOnCommand(hwnd, wParam, lParam)
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	case wmClose:
		pDestroyWindow.Call(uintptr(hwnd))
		return 0
	}
	r, _, _ := pDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func fbOnCreate(hwnd syscall.Handle) uintptr {
	font, _, _ := pGetStockObject.Call(uintptr(defaultGuiFont))
	fh := syscall.Handle(font)

	// Description label
	descLbl, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("STATIC"))),
		uintptr(unsafe.Pointer(utf16Ptr("请描述您的建议或问题："))),
		uintptr(wsChild|wsVisible|ssLeft),
		uintptr(fbMargin), uintptr(fbMargin),
		uintptr(fbEdtW), uintptr(fbLblH),
		uintptr(hwnd), 0, uintptr(fbHinst), 0,
	)
	pSendMessage.Call(descLbl, uintptr(wmSetFont), uintptr(fh), 1)

	// Multiline edit
	editStyle := uintptr(wsChild | wsVisible | wsBorder | wsTabStop | esLeft |
		esMultiline | esWantReturn | esAutoVScroll | wsVScroll)
	edt, _, _ := pCreateWindowEx.Call(
		uintptr(wsExClientEdge),
		uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
		0,
		editStyle,
		uintptr(fbMargin), uintptr(fbEdtY),
		uintptr(fbEdtW), uintptr(fbEdtH),
		uintptr(hwnd), uintptr(fbCtlEdit), uintptr(fbHinst), 0,
	)
	pSendMessage.Call(edt, uintptr(wmSetFont), uintptr(fh), 1)
	fbEditHwnd = syscall.Handle(edt)

	// Buttons — right-aligned
	okX := int32(fbFormW - fbMargin - fbBtnW)
	cancelX := okX - fbBtnW - fbBtnGap

	pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("提交"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsDefPushButton),
		uintptr(okX), uintptr(fbBtnY), uintptr(fbBtnW), uintptr(fbBtnH),
		uintptr(hwnd), uintptr(fbCtlSubmitBtn), uintptr(fbHinst), 0,
	)

	pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("取消"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsPushButton),
		uintptr(cancelX), uintptr(fbBtnY), uintptr(fbBtnW), uintptr(fbBtnH),
		uintptr(hwnd), uintptr(fbCtlCancelBtn), uintptr(fbHinst), 0,
	)

	return 0
}

func fbOnCommand(hwnd syscall.Handle, wParam, lParam uintptr) uintptr {
	ctlID := uint16(wParam & 0xFFFF)
	_ = lParam

	switch ctlID {
	case fbCtlSubmitBtn:
		text := strings.TrimSpace(getWindowText(fbEditHwnd))
		if text == "" {
			windows.MessageBox(windows.HWND(hwnd),
				windows.StringToUTF16Ptr("反馈内容不能为空，请输入您的建议或问题。"),
				windows.StringToUTF16Ptr("Sypora - 反馈"),
				windows.MB_OK|windows.MB_ICONWARNING,
			)
			pSetFocus.Call(uintptr(fbEditHwnd))
			return 0
		}
		fbText = text
		fbSaved = true
		pDestroyWindow.Call(uintptr(hwnd))

	case fbCtlCancelBtn:
		fbSaved = false
		pDestroyWindow.Call(uintptr(hwnd))
	}

	return 0
}
