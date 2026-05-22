//go:build windows

package tray

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"sypora/internal/config"

	"github.com/ncruces/zenity"

	"golang.org/x/sys/windows"
)

// ---------------------------------------------------------------------------
// WorkDir form constants
// ---------------------------------------------------------------------------

const (
	maxWorkRows = 8
	ctlsPerRow  = 6 // lblLocal, edtLocal, btnLocal, lblRemote, edtRemote, btnRemote

	wdCtlBase       = 200
	wdCtlAddBtn     = 301
	wdCtlOKBtn      = 302
	wdCtlCancelBtn  = 303
	wdCtlDeleteMenu = 304
)

// Layout — margins match S3 form style (20px left / 25px right)
const (
	wdFormW     = 640
	wdFormBaseH = 155
	wdRowH      = 34

	wdMarginL  = 20 // left margin, matches S3 form lblX
	wdMarginR  = 25 // right margin
	wdContentR = wdFormW - wdMarginR // 615 — right edge of content area

	wdLblW    = 35
	wdGap     = 6 // spacing between sibling controls
	wdBtnW    = 24
	wdArrowW  = 14
	wdBtnWide = 90 // OK/Cancel button width
	wdBtnGap  = 8  // gap between bottom buttons

	// Computed X positions (left to right):
	wdEdtLocalX  = wdMarginL + wdLblW + wdGap          // 61
	wdEdtW       = 213
	wdBtnLocalX  = wdEdtLocalX + wdEdtW + wdGap        // 280
	wdArrowX     = wdBtnLocalX + wdBtnW + wdGap        // 310
	wdLblRemoteX = wdArrowX + wdArrowW + wdGap         // 330
	wdEdtRemoteX = wdLblRemoteX + wdLblW + wdGap       // 371
	wdBtnRemoteX = wdContentR - wdBtnW                 // 591

	wdAddBtnY   = 14
	wdAddBtnW   = 110
	wdAddBtnH   = 26
	wdRowStartY = 50
	wdBtnYPad   = 16
	wdBtnH      = 28
)

// ---------------------------------------------------------------------------
// Additional Win32 procs for context menu
// ---------------------------------------------------------------------------

var (
	pCreatePopupMenu = user32.NewProc("CreatePopupMenu")
	pAppendMenuW     = user32.NewProc("AppendMenuW")
	pTrackPopupMenu  = user32.NewProc("TrackPopupMenu")
	pDestroyMenu     = user32.NewProc("DestroyMenu")
	pClientToScreen  = user32.NewProc("ClientToScreen")
)

type point struct {
	x int32
	y int32
}

// ---------------------------------------------------------------------------
// Global state
// ---------------------------------------------------------------------------

var (
	wdClassOnce  sync.Once
	wdWndProc    uintptr
	wdClassName  *uint16
	wdClassAtom  uintptr

	gWDirs       []config.WorkDir
	gWDSaved     bool
	gWDHwnd      syscall.Handle
	gWDHinst     syscall.Handle
	gWDRowCount  int
	gWDCtxRow    = -1
)

type rowControls struct {
	lblLocal  syscall.Handle
	edtLocal  syscall.Handle
	btnLocal  syscall.Handle
	arrow     syscall.Handle
	lblRemote syscall.Handle
	edtRemote syscall.Handle
	btnRemote syscall.Handle
}

var gWDRows [maxWorkRows]rowControls

// ---------------------------------------------------------------------------
// showWorkDirForm — modal Win32 form
// ---------------------------------------------------------------------------

func showWorkDirForm(dirs []config.WorkDir) ([]config.WorkDir, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	gWDirs = make([]config.WorkDir, len(dirs))
	copy(gWDirs, dirs)
	gWDSaved = false
	gWDCtxRow = -1
	gWDRowCount = len(dirs)
	if gWDRowCount < 1 {
		gWDRowCount = 1
	}

	hinst, _, _ := pGetModuleHandle.Call(0)
	gWDHinst = syscall.Handle(hinst)

	wdClassOnce.Do(func() {
		wdClassName = utf16Ptr("SyporaWorkDirForm")
		wdWndProc = syscall.NewCallback(wdWindowProc)

		brush := appBackgroundBrush()
		arrowCursor, _, _ := pLoadCursor.Call(0, uintptr(idcArrow))

		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   wdWndProc,
			hInstance:     gWDHinst,
			hCursor:       syscall.Handle(arrowCursor),
			hbrBackground: syscall.Handle(brush),
			lpszClassName: wdClassName,
		}
		r, _, _ := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		wdClassAtom = r
	})

	formH := int32(wdFormBaseH + gWDRowCount*wdRowH)
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsVisible | wsDlgFrame)
	exStyle := uintptr(wsExDlgModalFrame | wsExControlParent)

	r, _, _ := pCreateWindowEx.Call(
		exStyle,
		uintptr(unsafe.Pointer(wdClassName)),
		uintptr(unsafe.Pointer(utf16Ptr("工作目录配置"))),
		style,
		uintptr(cwUseDefault), uintptr(cwUseDefault),
		uintptr(wdFormW), uintptr(formH),
		0, 0, uintptr(gWDHinst), 0,
	)
	if r == 0 {
		return dirs, false
	}
	gWDHwnd = syscall.Handle(r)

	centerWindowOnScreen(gWDHwnd)
	applyRoundedCorners(gWDHwnd, wdFormW, formH, 8)
	pShowWindow.Call(uintptr(gWDHwnd), uintptr(windows.SW_SHOW))
	pUpdateWindow.Call(uintptr(gWDHwnd))

	// Add button (left-aligned to content margin)
	pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("＋ 添加目录"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsPushButton),
		uintptr(wdMarginL), uintptr(wdAddBtnY),
		uintptr(wdAddBtnW), uintptr(wdAddBtnH),
		uintptr(gWDHwnd), uintptr(wdCtlAddBtn), uintptr(gWDHinst), 0,
	)

	// Create row controls
	for i := 0; i < maxWorkRows; i++ {
		createRowControls(i)
		if i >= gWDRowCount {
			hideRow(i)
		} else {
			populateRow(i)
		}
	}

	// Bottom buttons — right-aligned to content edge
	btnY := int32(wdRowStartY + gWDRowCount*wdRowH + wdBtnYPad)
	okX := int32(wdContentR - wdBtnWide)
	cancelX := okX - wdBtnWide - wdBtnGap

	pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("确定"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsDefPushButton),
		uintptr(okX), uintptr(btnY), uintptr(wdBtnWide), uintptr(wdBtnH),
		uintptr(gWDHwnd), uintptr(wdCtlOKBtn), uintptr(gWDHinst), 0,
	)

	pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("取消"))),
		uintptr(wsChild|wsVisible|wsTabStop|bsPushButton),
		uintptr(cancelX), uintptr(btnY), uintptr(wdBtnWide), uintptr(wdBtnH),
		uintptr(gWDHwnd), uintptr(wdCtlCancelBtn), uintptr(gWDHinst), 0,
	)

	// Message loop
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
		dlg, _, _ := pIsDialogMessage.Call(uintptr(gWDHwnd), uintptr(unsafe.Pointer(&msg)))
		if dlg == 0 {
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			pDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}

	if gWDSaved {
		return gWDirs, true
	}
	return dirs, false
}

// ---------------------------------------------------------------------------
// Row control management
// ---------------------------------------------------------------------------

func rowControlID(row, sub int) int32 {
	return int32(wdCtlBase + row*ctlsPerRow + sub)
}

func createRowControls(row int) {
	inst := uintptr(gWDHinst)
	font, _, _ := pGetStockObject.Call(uintptr(defaultGuiFont))
	fh := syscall.Handle(font)
	y := int32(wdRowStartY + row*wdRowH)
	parent := uintptr(gWDHwnd)

	// Local label
	lblL, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("STATIC"))),
		uintptr(unsafe.Pointer(utf16Ptr("本地:"))),
		uintptr(wsChild|wsVisible|ssLeft),
		uintptr(wdMarginL), uintptr(y+4), uintptr(wdLblW), uintptr(20),
		parent, uintptr(rowControlID(row, 0)), inst, 0,
	)
	pSendMessage.Call(lblL, uintptr(wmSetFont), uintptr(fh), 1)
	gWDRows[row].lblLocal = syscall.Handle(lblL)

	// Local edit
	edtL, _, _ := pCreateWindowEx.Call(
		uintptr(wsExClientEdge),
		uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
		0,
		uintptr(wsChild|wsVisible|wsBorder|wsTabStop|esLeft|esAutoHScroll),
		uintptr(wdEdtLocalX), uintptr(y+1), uintptr(wdEdtW), uintptr(22),
		parent, uintptr(rowControlID(row, 1)), inst, 0,
	)
	pSendMessage.Call(edtL, uintptr(wmSetFont), uintptr(fh), 1)
	gWDRows[row].edtLocal = syscall.Handle(edtL)

	// Local browse "..."
	btnL, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("..."))),
		uintptr(wsChild|wsVisible|wsTabStop|bsPushButton),
		uintptr(wdBtnLocalX), uintptr(y), uintptr(wdBtnW), uintptr(22),
		parent, uintptr(rowControlID(row, 2)), inst, 0,
	)
	pSendMessage.Call(btnL, uintptr(wmSetFont), uintptr(fh), 1)
	gWDRows[row].btnLocal = syscall.Handle(btnL)

	// Arrow
	arr, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("STATIC"))),
		uintptr(unsafe.Pointer(utf16Ptr("→"))),
		uintptr(wsChild|wsVisible|ssLeft),
		uintptr(wdArrowX), uintptr(y+4), uintptr(wdArrowW), uintptr(20),
		parent, 0, inst, 0,
	)
	pSendMessage.Call(arr, uintptr(wmSetFont), uintptr(fh), 1)
	gWDRows[row].arrow = syscall.Handle(arr)

	// Remote label
	lblR, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("STATIC"))),
		uintptr(unsafe.Pointer(utf16Ptr("远程:"))),
		uintptr(wsChild|wsVisible|ssLeft),
		uintptr(wdLblRemoteX), uintptr(y+4), uintptr(wdLblW), uintptr(20),
		parent, uintptr(rowControlID(row, 3)), inst, 0,
	)
	pSendMessage.Call(lblR, uintptr(wmSetFont), uintptr(fh), 1)
	gWDRows[row].lblRemote = syscall.Handle(lblR)

	// Remote edit
	edtR, _, _ := pCreateWindowEx.Call(
		uintptr(wsExClientEdge),
		uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
		0,
		uintptr(wsChild|wsVisible|wsBorder|wsTabStop|esLeft|esAutoHScroll),
		uintptr(wdEdtRemoteX), uintptr(y+1), uintptr(wdEdtW), uintptr(22),
		parent, uintptr(rowControlID(row, 4)), inst, 0,
	)
	pSendMessage.Call(edtR, uintptr(wmSetFont), uintptr(fh), 1)
	gWDRows[row].edtRemote = syscall.Handle(edtR)

	// Remote browse "..."
	btnR, _, _ := pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16Ptr("BUTTON"))),
		uintptr(unsafe.Pointer(utf16Ptr("..."))),
		uintptr(wsChild|wsVisible|wsTabStop|bsPushButton),
		uintptr(wdBtnRemoteX), uintptr(y), uintptr(wdBtnW), uintptr(22),
		parent, uintptr(rowControlID(row, 5)), inst, 0,
	)
	pSendMessage.Call(btnR, uintptr(wmSetFont), uintptr(fh), 1)
	gWDRows[row].btnRemote = syscall.Handle(btnR)
}

func populateRow(row int) {
	if row < len(gWDirs) {
		setWindowText(gWDRows[row].edtLocal, gWDirs[row].LocalPath)
		setWindowText(gWDRows[row].edtRemote, gWDirs[row].RemotePath)
	} else {
		setWindowText(gWDRows[row].edtLocal, "")
		setWindowText(gWDRows[row].edtRemote, "")
	}
}

func hideRow(row int) {
	hide := func(h syscall.Handle) {
		if h != 0 {
			pShowWindow.Call(uintptr(h), 0)
		}
	}
	hide(gWDRows[row].lblLocal)
	hide(gWDRows[row].edtLocal)
	hide(gWDRows[row].btnLocal)
	hide(gWDRows[row].arrow)
	hide(gWDRows[row].lblRemote)
	hide(gWDRows[row].edtRemote)
	hide(gWDRows[row].btnRemote)
}

func showRow(row int) {
	sw := uintptr(5)
	show := func(h syscall.Handle) {
		if h != 0 {
			pShowWindow.Call(uintptr(h), sw)
		}
	}
	show(gWDRows[row].lblLocal)
	show(gWDRows[row].edtLocal)
	show(gWDRows[row].btnLocal)
	show(gWDRows[row].arrow)
	show(gWDRows[row].lblRemote)
	show(gWDRows[row].edtRemote)
	show(gWDRows[row].btnRemote)
}

// ---------------------------------------------------------------------------
// Row operations
// ---------------------------------------------------------------------------

func addBlankRow() {
	if gWDRowCount >= maxWorkRows {
		windows.MessageBox(windows.HWND(gWDHwnd),
			windows.StringToUTF16Ptr("已达到最大目录数量限制。"),
			windows.StringToUTF16Ptr("Sypora - 工作目录"),
			windows.MB_OK|windows.MB_ICONINFORMATION,
		)
		return
	}
	gWDirs = append(gWDirs, config.WorkDir{})
	showRow(gWDRowCount)
	populateRow(gWDRowCount)
	gWDRowCount++
	resizeWindow()
}

func deleteRow(row int) {
	if row < 0 || row >= gWDRowCount {
		return
	}
	if row < len(gWDirs) {
		gWDirs = append(gWDirs[:row], gWDirs[row+1:]...)
	}
	gWDRowCount--
	for i := row; i < gWDRowCount; i++ {
		populateRow(i)
	}
	if gWDRowCount < maxWorkRows {
		hideRow(gWDRowCount)
	}
	if gWDRowCount < 1 {
		gWDRowCount = 1
		gWDirs = []config.WorkDir{{}}
		showRow(0)
		populateRow(0)
	}
	resizeWindow()
}

func resizeWindow() {
	newFormH := int32(wdFormBaseH + gWDRowCount*wdRowH)
	// SWP_NOMOVE | SWP_NOZORDER — resize only
	pSetWindowPos.Call(uintptr(gWDHwnd), 0, 0, 0,
		uintptr(wdFormW), uintptr(newFormH),
		uintptr(0x0002|0x0004))
	applyRoundedCorners(gWDHwnd, wdFormW, newFormH, 8)
	centerWindowOnScreen(gWDHwnd)

	btnY := int32(wdRowStartY + gWDRowCount*wdRowH + wdBtnYPad)
	okX := int32(wdContentR - wdBtnWide)
	cancelX := okX - wdBtnWide - wdBtnGap
	okBtn := getDlgItem(gWDHwnd, wdCtlOKBtn)
	cancelBtn := getDlgItem(gWDHwnd, wdCtlCancelBtn)
	// SWP_NOSIZE | SWP_NOZORDER — move only
	pSetWindowPos.Call(uintptr(okBtn), 0, uintptr(okX), uintptr(btnY), 0, 0, uintptr(0x0001|0x0004))
	pSetWindowPos.Call(uintptr(cancelBtn), 0, uintptr(cancelX), uintptr(btnY), 0, 0, uintptr(0x0001|0x0004))
}

func rowFromY(y int32) int {
	relY := y - int32(wdRowStartY)
	if relY < 0 {
		return -1
	}
	r := int(relY) / wdRowH
	if r >= gWDRowCount {
		return -1
	}
	return r
}

// ---------------------------------------------------------------------------
// Save / validate
// ---------------------------------------------------------------------------

func saveWorkDirs() bool {
	for i := 0; i < gWDRowCount; i++ {
		local := strings.TrimSpace(getWindowText(gWDRows[i].edtLocal))
		remote := strings.TrimSpace(getWindowText(gWDRows[i].edtRemote))

		if local == "" && remote == "" {
			continue
		}
		if local == "" {
			windows.MessageBox(windows.HWND(gWDHwnd),
				windows.StringToUTF16Ptr("第 "+strconv.Itoa(i+1)+" 行的本地目录未配置。\n请选择本地工作目录。"),
				windows.StringToUTF16Ptr("Sypora - 配置不完整"),
				windows.MB_OK|windows.MB_ICONWARNING,
			)
			pSetFocus.Call(uintptr(gWDRows[i].edtLocal))
			return false
		}
		if remote == "" {
			windows.MessageBox(windows.HWND(gWDHwnd),
				windows.StringToUTF16Ptr("第 "+strconv.Itoa(i+1)+" 行的远程前缀未配置。\n请输入 S3 远程路径前缀。"),
				windows.StringToUTF16Ptr("Sypora - 配置不完整"),
				windows.MB_OK|windows.MB_ICONWARNING,
			)
			pSetFocus.Call(uintptr(gWDRows[i].edtRemote))
			return false
		}
	}

	var result []config.WorkDir
	for i := 0; i < gWDRowCount; i++ {
		local := strings.TrimSpace(getWindowText(gWDRows[i].edtLocal))
		remote := strings.TrimSpace(getWindowText(gWDRows[i].edtRemote))
		if local == "" && remote == "" {
			continue
		}
		remote = strings.TrimLeft(remote, "/")
		if !strings.HasSuffix(remote, "/") {
			remote += "/"
		}
		result = append(result, config.WorkDir{LocalPath: local, RemotePath: remote})
	}
	gWDirs = result
	gWDSaved = true
	return true
}

// ---------------------------------------------------------------------------
// Browse handlers
// ---------------------------------------------------------------------------

func browseLocalFolder(row int) {
	folder, err := zenity.SelectFile(
		zenity.Title("选择工作目录"),
		zenity.Directory(),
	)
	if err != nil || folder == "" {
		return
	}
	setWindowText(gWDRows[row].edtLocal, folder)
	if strings.TrimSpace(getWindowText(gWDRows[row].edtRemote)) == "" {
		setWindowText(gWDRows[row].edtRemote, filepath.Base(folder)+"/")
	}
}

func browseRemotePrefix(row int) {
	current := strings.TrimSpace(getWindowText(gWDRows[row].edtRemote))
	prefix, err := zenity.Entry("输入 S3 上对应的前缀：",
		zenity.Title("Sypora - 远程路径"),
		zenity.EntryText(current),
	)
	if err != nil || prefix == "" {
		return
	}
	setWindowText(gWDRows[row].edtRemote, strings.TrimSpace(prefix))
}

// ---------------------------------------------------------------------------
// Window procedure
// ---------------------------------------------------------------------------

func wdWindowProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmCommand:
		return wdOnCommand(hwnd, wParam, lParam)
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	case wmClose:
		pDestroyWindow.Call(uintptr(hwnd))
		return 0
	case 0x0204: // WM_RBUTTONUP
		return wdOnRButtonUp(hwnd, lParam)
	}
	r, _, _ := pDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func wdOnCommand(hwnd syscall.Handle, wParam, lParam uintptr) uintptr {
	ctlID := uint16(wParam & 0xFFFF)
	_ = lParam

	switch ctlID {
	case uint16(wdCtlAddBtn):
		addBlankRow()

	case uint16(wdCtlOKBtn):
		if saveWorkDirs() {
			pDestroyWindow.Call(uintptr(hwnd))
		}

	case uint16(wdCtlCancelBtn):
		gWDSaved = false
		pDestroyWindow.Call(uintptr(hwnd))

	case uint16(wdCtlDeleteMenu):
		if gWDCtxRow >= 0 && gWDCtxRow < gWDRowCount {
			deleteRow(gWDCtxRow)
		}

	default:
		if ctlID >= uint16(wdCtlBase) && ctlID < uint16(wdCtlBase+maxWorkRows*ctlsPerRow) {
			offset := int(ctlID) - wdCtlBase
			row := offset / ctlsPerRow
			sub := offset % ctlsPerRow
			switch sub {
			case 2:
				browseLocalFolder(row)
			case 5:
				browseRemotePrefix(row)
			}
		}
	}
	return 0
}

func wdOnRButtonUp(hwnd syscall.Handle, lParam uintptr) uintptr {
	x := int32(lParam & 0xFFFF)
	y := int32((lParam >> 16) & 0xFFFF)

	row := rowFromY(y)
	if row < 0 || row >= gWDRowCount {
		return 0
	}
	gWDCtxRow = row

	hmenu, _, _ := pCreatePopupMenu.Call()
	if hmenu == 0 {
		return 0
	}
	defer pDestroyMenu.Call(hmenu)

	pAppendMenuW.Call(hmenu, 0, uintptr(wdCtlDeleteMenu),
		uintptr(unsafe.Pointer(utf16Ptr("删除此目录"))))

	var pt point
	pt.x = x
	pt.y = y
	pClientToScreen.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))

	pTrackPopupMenu.Call(hmenu, uintptr(0x0002), // TPM_RIGHTBUTTON
		uintptr(pt.x), uintptr(pt.y), 0, uintptr(hwnd), 0)

	return 0
}
