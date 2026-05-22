//go:build windows

package tray

import (
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"sypora/internal/config"
	"sypora/internal/s3client"

	"golang.org/x/sys/windows"
)

// ---------------------------------------------------------------------------
// Win32 constants we need (not present in golang.org/x/sys/windows)
// ---------------------------------------------------------------------------

const (
	// Window styles
	wsOverlapped = 0x00000000
	wsChild      = 0x40000000
	wsVisible    = 0x10000000
	wsBorder     = 0x00800000
	wsTabStop    = 0x00010000
	wsCaption    = 0x00C00000
	wsSysMenu    = 0x00080000
	wsDlgFrame   = 0x00400000

	// Extended window styles
	wsExDlgModalFrame = 0x00000001
	wsExClientEdge    = 0x00000200
	wsExControlParent = 0x00010000

	// Button styles
	bsPushButton   = 0x00000000
	bsDefPushButton = 0x00000001
	bsAutoCheckbox = 0x00000003

	// Edit styles
	esLeft        = 0x0000
	esAutoHScroll = 0x0080

	// Static styles
	ssLeft = 0x0000

	// Window messages
	wmCreate  = 0x0001
	wmDestroy = 0x0002
	wmClose   = 0x0010
	wmSetFont = 0x0030
	wmCommand = 0x0111

	// Button messages
	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1

	// Check states
	bstChecked   = 0x0001
	bstUnchecked = 0x0000

	// Stock objects
	defaultGuiFont = 17

	// System colors
	colorBtnFace = 15

	// Sys metrics
	smCxScreen = 0
	smCyScreen = 1

	// Standard cursors
	idcArrow = 32512

	// SetWindowPos
	swpNoSize   = 0x0001
	swpNoZOrder = 0x0004

	// Predefined window handles
	cwUseDefault = 0x80000000
)

// Control IDs
const (
	ctlEndpoint  = 101
	ctlAccessKey = 102
	ctlSecretKey = 103
	ctlBucket    = 104
	ctlRegion    = 105
	ctlUseSSL    = 106
	ctlTestBtn   = 107
	ctlOKBtn     = 108
	ctlCancelBtn = 109
)

// Layout (pixels)
const (
	formW = 480
	formH = 320

	lblX = 20
	lblW = 110
	edtX = 135
	edtW = 320

	rowH = 32
	row0 = 24

	btnW = 90
	btnH = 28
	btnGap = 8
	btnY = 239
)

// ---------------------------------------------------------------------------
// Win32 types we need
// ---------------------------------------------------------------------------

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type rect struct {
	left   int32
	top    int32
	right  int32
	bottom int32
}

// ---------------------------------------------------------------------------
// DLL proc pointers
// ---------------------------------------------------------------------------

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pGetMessage       = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pGetWindowText    = user32.NewProc("GetWindowTextW")
	pSetWindowText    = user32.NewProc("SetWindowTextW")
	pSetFocus         = user32.NewProc("SetFocus")
	pGetDlgItem       = user32.NewProc("GetDlgItem")
	pSendMessage      = user32.NewProc("SendMessageW")
	pLoadCursor       = user32.NewProc("LoadCursorW")
	pIsDialogMessage  = user32.NewProc("IsDialogMessageW")
	pGetSysColorBrush = user32.NewProc("GetSysColorBrush")
	pSetWindowPos     = user32.NewProc("SetWindowPos")
	pGetWindowRect    = user32.NewProc("GetWindowRect")
	pShowWindow       = user32.NewProc("ShowWindow")
	pUpdateWindow     = user32.NewProc("UpdateWindow")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")

	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")

	pGetStockObject = gdi32.NewProc("GetStockObject")

	pCreateSolidBrush  = gdi32.NewProc("CreateSolidBrush")
	pCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	pSetWindowRgn      = user32.NewProc("SetWindowRgn")
)

// ---------------------------------------------------------------------------
// Global state (only one modal form open at a time — safe)
// ---------------------------------------------------------------------------

var (
	formClassOnce   sync.Once
	formWndProc     uintptr // persistent reference so GC doesn't collect the callback
	formClassName   *uint16
	formClassAtom   uintptr // non-zero if class registered successfully

	gFormCfg   config.S3Config
	gFormSaved bool

	gFormHwnd  syscall.Handle
	gFormEdits [5]syscall.Handle
	gFormHinst syscall.Handle
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func utf16Ptr(s string) *uint16 {
	if s == "" {
		return nil
	}
	return windows.StringToUTF16Ptr(s)
}

func getWindowText(hwnd syscall.Handle) string {
	buf := make([]uint16, 1024)
	n, _, _ := pGetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func setWindowText(hwnd syscall.Handle, text string) {
	pSetWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(utf16Ptr(text))))
}

func getDlgItem(parent syscall.Handle, id int32) syscall.Handle {
	r, _, _ := pGetDlgItem.Call(uintptr(parent), uintptr(id))
	return syscall.Handle(r)
}

func isDlgButtonChecked(parent syscall.Handle, id int32) bool {
	btn := getDlgItem(parent, id)
	r, _, _ := pSendMessage.Call(uintptr(btn), uintptr(bmGetCheck), 0, 0)
	return r == bstChecked
}

func setDlgButtonCheck(parent syscall.Handle, id int32, checked bool) {
	btn := getDlgItem(parent, id)
	v := uintptr(bstUnchecked)
	if checked {
		v = bstChecked
	}
	pSendMessage.Call(uintptr(btn), uintptr(bmSetCheck), v, 0)
}

// ---------------------------------------------------------------------------
// Shared: background brush (#F2F2F2)
// ---------------------------------------------------------------------------

var (
	appBgBrush     uintptr
	appBgBrushOnce sync.Once
)

func appBackgroundBrush() uintptr {
	appBgBrushOnce.Do(func() {
		// #F2F2F2 in COLORREF = 0x00F2F2F2 (BBGGRR)
		b, _, _ := pCreateSolidBrush.Call(0x00F2F2F2)
		appBgBrush = b
	})
	return appBgBrush
}

// ---------------------------------------------------------------------------
// Shared: rounded corners (8px radius)
// ---------------------------------------------------------------------------

func applyRoundedCorners(hwnd syscall.Handle, w, h, r int32) {
	rgn, _, _ := pCreateRoundRectRgn.Call(0, 0, uintptr(w), uintptr(h), uintptr(r), uintptr(r))
	pSetWindowRgn.Call(uintptr(hwnd), rgn, 1) // TRUE = redraw
}

// ---------------------------------------------------------------------------
// showS3Form — modal Win32 form, returns (config, saved)
// ---------------------------------------------------------------------------

func showS3Form(cfg config.S3Config) (config.S3Config, bool) {
	// Lock the goroutine to this OS thread — Windows message queues are per-thread.
	// Without this, Go can reschedule the goroutine mid-message-loop, causing crashes.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	gFormCfg = cfg
	gFormSaved = false

	hinst, _, _ := pGetModuleHandle.Call(0)
	gFormHinst = syscall.Handle(hinst)

	// -- register window class (once) --
	formClassOnce.Do(func() {
		formClassName = utf16Ptr("SyporaS3Form")
		formWndProc = syscall.NewCallback(wndProc)

		brush := appBackgroundBrush()
		arrowCursor, _, _ := pLoadCursor.Call(0, uintptr(idcArrow))

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   formWndProc,
			hInstance:     gFormHinst,
			hCursor:       syscall.Handle(arrowCursor),
			hbrBackground: syscall.Handle(brush),
			lpszClassName: formClassName,
		}
		r, _, _ := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		formClassAtom = r
	})

	// -- create window --
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsVisible | wsDlgFrame)
	exStyle := uintptr(wsExDlgModalFrame | wsExControlParent)

	title := utf16Ptr("S3 服务器配置")

	r, _, _ := pCreateWindowEx.Call(
		exStyle,
		uintptr(unsafe.Pointer(formClassName)),
		uintptr(unsafe.Pointer(title)),
		style,
		uintptr(cwUseDefault), uintptr(cwUseDefault),
		uintptr(formW), uintptr(formH),
		0, 0, uintptr(gFormHinst), 0,
	)
	if r == 0 {
		return cfg, false
	}
	gFormHwnd = syscall.Handle(r)

	// -- center on screen --
	centerWindowOnScreen(gFormHwnd)

	applyRoundedCorners(gFormHwnd, formW, formH, 8)

	pShowWindow.Call(uintptr(gFormHwnd), uintptr(windows.SW_SHOW))
	pUpdateWindow.Call(uintptr(gFormHwnd))

	// -- modal message loop --
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
		if ret == 0 { // WM_QUIT
			break
		}
		if int32(ret) == -1 {
			break
		}
		// IsDialogMessage for tab navigation and default button
		dlg, _, _ := pIsDialogMessage.Call(uintptr(gFormHwnd), uintptr(unsafe.Pointer(&msg)))
		if dlg == 0 {
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			pDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}

	if gFormSaved {
		return gFormCfg, true
	}
	return cfg, false
}

func centerWindowOnScreen(hwnd syscall.Handle) {
	var rc rect
	pGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))
	w := rc.right - rc.left
	h := rc.bottom - rc.top

	sw, _, _ := pGetSystemMetrics.Call(uintptr(smCxScreen))
	sh, _, _ := pGetSystemMetrics.Call(uintptr(smCyScreen))

	x := (int32(sw) - w) / 2
	y := (int32(sh) - h) / 2

	pSetWindowPos.Call(uintptr(hwnd), 0,
		uintptr(x), uintptr(y), 0, 0,
		uintptr(swpNoSize|swpNoZOrder))
}

// ---------------------------------------------------------------------------
// Window procedure
// ---------------------------------------------------------------------------

func wndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCreate:
		return onCreate(hwnd)
	case wmCommand:
		return onCommand(hwnd, wParam, lParam)
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

// ---------------------------------------------------------------------------
// onCreate — create all child controls
// ---------------------------------------------------------------------------

func onCreate(hwnd syscall.Handle) uintptr {
	font, _, _ := pGetStockObject.Call(uintptr(defaultGuiFont))
	fontHandle := syscall.Handle(font)

	// Labels and edit fields
	labels := []string{"Endpoint*:", "Access Key*:", "Secret Key*:", "Bucket*:", "Region*:"}
	for i, label := range labels {
		y := int32(row0 + i*rowH)

		// label
		lbl, _, _ := pCreateWindowEx.Call(0,
			uintptr(unsafe.Pointer(utf16Ptr("STATIC"))),
			uintptr(unsafe.Pointer(utf16Ptr(label))),
			uintptr(wsChild|wsVisible|ssLeft),
			uintptr(lblX), uintptr(y),
			uintptr(lblW), uintptr(23),
			uintptr(hwnd), 0, uintptr(gFormHinst), 0,
		)
		pSendMessage.Call(lbl, uintptr(wmSetFont), uintptr(fontHandle), 1)

		// edit
		edt, _, _ := pCreateWindowEx.Call(
			uintptr(wsExClientEdge),
			uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
			0,
			uintptr(wsChild|wsVisible|wsBorder|wsTabStop|esLeft|esAutoHScroll),
			uintptr(edtX), uintptr(y),
			uintptr(edtW), uintptr(23),
			uintptr(hwnd), uintptr(ctlEndpoint+i), uintptr(gFormHinst), 0,
		)
		pSendMessage.Call(edt, uintptr(wmSetFont), uintptr(fontHandle), 1)
		gFormEdits[i] = syscall.Handle(edt)
	}

	// SSL checkbox
	sslY := int32(row0 + 5*rowH + 5)
	cb, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("使用 HTTPS/SSL 连接"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsAutoCheckbox),
		uintptr(lblX), uintptr(sslY),
		uintptr(220), uintptr(23),
		uintptr(hwnd), uintptr(ctlUseSSL), uintptr(gFormHinst), 0,
	)
	pSendMessage.Call(cb, uintptr(wmSetFont), uintptr(fontHandle), 1)

	// Buttons
	okX := int32(edtX + edtW - btnW)
	testX := okX - int32(btnW) - int32(btnGap)
	cancelX := testX - int32(btnW) - int32(btnGap)

	// OK (default)
	okBtn, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("确定"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsDefPushButton),
		uintptr(okX), uintptr(btnY), uintptr(btnW), uintptr(btnH),
		uintptr(hwnd), uintptr(ctlOKBtn), uintptr(gFormHinst), 0,
	)
	pSendMessage.Call(okBtn, uintptr(wmSetFont), uintptr(fontHandle), 1)

	// Test
	pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("测试连接"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsPushButton),
		uintptr(testX), uintptr(btnY), uintptr(btnW), uintptr(btnH),
		uintptr(hwnd), uintptr(ctlTestBtn), uintptr(gFormHinst), 0,
	)
	pSendMessage.Call(uintptr(getDlgItem(hwnd, ctlTestBtn)), uintptr(wmSetFont), uintptr(fontHandle), 1)

	// Cancel
	pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("取消"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsPushButton),
		uintptr(cancelX), uintptr(btnY), uintptr(btnW), uintptr(btnH),
		uintptr(hwnd), uintptr(ctlCancelBtn), uintptr(gFormHinst), 0,
	)
	pSendMessage.Call(uintptr(getDlgItem(hwnd, ctlCancelBtn)), uintptr(wmSetFont), uintptr(fontHandle), 1)

	// Populate current values
	populateFormFields(hwnd)

	return 0
}

// ---------------------------------------------------------------------------
// onCommand — button / control notifications
// ---------------------------------------------------------------------------

func onCommand(hwnd syscall.Handle, wParam, lParam uintptr) uintptr {
	// Low word = control ID, high word = notification code
	ctlID := uint16(wParam & 0xFFFF)
	notify := uint16((wParam >> 16) & 0xFFFF)

	_ = notify
	_ = lParam

	switch ctlID {
	case ctlOKBtn:
		if validateAndCollect() {
			gFormSaved = true
			pDestroyWindow.Call(uintptr(hwnd))
		}

	case ctlTestBtn:
		if validateAndCollect() {
			testS3ConnectionFromForm()
		}

	case ctlCancelBtn:
		gFormSaved = false
		pDestroyWindow.Call(uintptr(hwnd))

	case ctlUseSSL:
		// Handled by bsAutoCheckbox automatically
	}

	return 0
}

// ---------------------------------------------------------------------------
// Field helpers
// ---------------------------------------------------------------------------

func populateFormFields(dialog syscall.Handle) {
	cfg := gFormCfg
	setWindowText(gFormEdits[0], cfg.Endpoint)
	setWindowText(gFormEdits[1], cfg.AccessKey)
	setWindowText(gFormEdits[2], cfg.SecretKey)
	setWindowText(gFormEdits[3], cfg.Bucket)
	setWindowText(gFormEdits[4], cfg.Region)
	setDlgButtonCheck(dialog, ctlUseSSL, cfg.UseSSL)
}

func collectFields() config.S3Config {
	return config.S3Config{
		Endpoint:  strings.TrimSpace(getWindowText(gFormEdits[0])),
		AccessKey: strings.TrimSpace(getWindowText(gFormEdits[1])),
		SecretKey: strings.TrimSpace(getWindowText(gFormEdits[2])),
		Bucket:    strings.TrimSpace(getWindowText(gFormEdits[3])),
		Region:    strings.TrimSpace(getWindowText(gFormEdits[4])),
		UseSSL:    isDlgButtonChecked(gFormHwnd, ctlUseSSL),
	}
}

func validateAndCollect() bool {
	type field struct {
		hwnd  syscall.Handle
		name  string
		value string
	}
	fields := []field{
		{gFormEdits[0], "Endpoint", strings.TrimSpace(getWindowText(gFormEdits[0]))},
		{gFormEdits[1], "Access Key", strings.TrimSpace(getWindowText(gFormEdits[1]))},
		{gFormEdits[2], "Secret Key", strings.TrimSpace(getWindowText(gFormEdits[2]))},
		{gFormEdits[3], "Bucket", strings.TrimSpace(getWindowText(gFormEdits[3]))},
		{gFormEdits[4], "Region", strings.TrimSpace(getWindowText(gFormEdits[4]))},
	}

	alert := func(hwnd syscall.Handle, msg string) {
		windows.MessageBox(windows.HWND(gFormHwnd),
			windows.StringToUTF16Ptr(msg),
			windows.StringToUTF16Ptr("Sypora - 配置校验"),
			windows.MB_OK|windows.MB_ICONWARNING,
		)
		pSetFocus.Call(uintptr(hwnd))
	}

	hasWhitespace := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
	}
	hasControl := func(s string) bool {
		return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
	}

	for _, f := range fields {
		// 1. Non-empty after trim
		if f.value == "" {
			alert(f.hwnd, f.name+" 为必填项，请填写。")
			return false
		}

		// 2. No whitespace (for non-bucket fields, bucket allows dots/hyphens but not spaces)
		if hasWhitespace(f.value) {
			alert(f.hwnd, f.name+" 不能包含空格或制表符。")
			return false
		}

		// 3. No control characters
		if hasControl(f.value) {
			alert(f.hwnd, f.name+" 包含无效的控制字符，请移除。")
			return false
		}
	}

	// 4. Endpoint validation: hostname[:port] with optional scheme
	ep := fields[0].value
	if !validEndpoint(ep) {
		msg := "Endpoint 格式不正确。\n请输入有效的域名或IP地址，\n可带端口号（如 s3.example.com:9000）。\n不应包含路径、查询参数。"
		alert(fields[0].hwnd, msg)
		return false
	}

	// 5. Bucket validation
	bk := fields[3].value
	if !validBucketName(bk) {
		msg := "Bucket 名称格式不正确。\n要求：3-63 个字符，只能使用小写字母、\n数字、点号(.)和连字符(-)，\n必须以字母或数字开头和结尾，\n不能有连续的点号。"
		alert(fields[3].hwnd, msg)
		return false
	}

	gFormCfg = collectFields()
	return true
}

func validEndpoint(s string) bool {
	ep := s
	// Strip scheme prefix
	if strings.HasPrefix(ep, "https://") {
		ep = ep[8:]
	} else if strings.HasPrefix(ep, "http://") {
		ep = ep[7:]
	}
	if ep == "" {
		return false
	}
	// Find port
	host := ep
	if i := strings.LastIndexByte(ep, ':'); i >= 0 {
		host = ep[:i]
		port := ep[i+1:]
		if !isDigits(port) || len(port) == 0 {
			return false
		}
	}
	if host == "" {
		return false
	}
	// Host must not contain path/query/fragment chars
	if strings.ContainsAny(host, "/?#") {
		return false
	}
	// Must have at least one dot for DNS name, or be a valid IP
	if !strings.Contains(host, ".") {
		return false
	}
	// Validate hostname labels
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-') {
				return false
			}
		}
	}
	return true
}

func validBucketName(s string) bool {
	if len(s) < 3 || len(s) > 63 {
		return false
	}
	// Must start and end with letter or number
	if !isAlphaNum(rune(s[0])) || !isAlphaNum(rune(s[len(s)-1])) {
		return false
	}
	prevDot := false
	for _, ch := range s {
		if ch == '.' {
			if prevDot {
				return false // adjacent dots
			}
			prevDot = true
			continue
		}
		prevDot = false
		if !isAlphaNum(ch) && ch != '-' {
			return false
		}
	}
	return true
}

func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func testS3ConnectionFromForm() {
	cfg := gFormCfg
	client, err := s3client.New(cfg)
	if err != nil {
		windows.MessageBox(windows.HWND(gFormHwnd),
			windows.StringToUTF16Ptr("创建 S3 客户端失败: "+err.Error()),
			windows.StringToUTF16Ptr("Sypora - 连接测试"),
			windows.MB_OK|windows.MB_ICONERROR,
		)
		return
	}

	if err := client.TestConnection(); err != nil {
		windows.MessageBox(windows.HWND(gFormHwnd),
			windows.StringToUTF16Ptr("S3 连接测试失败:\n"+err.Error()),
			windows.StringToUTF16Ptr("Sypora - 连接测试"),
			windows.MB_OK|windows.MB_ICONERROR,
		)
		return
	}

	windows.MessageBox(windows.HWND(gFormHwnd),
		windows.StringToUTF16Ptr("S3 连接测试成功！"),
		windows.StringToUTF16Ptr("Sypora - 连接测试"),
		windows.MB_OK|windows.MB_ICONINFORMATION,
	)
}
