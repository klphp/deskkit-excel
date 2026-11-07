//go:build darwin

package window

/*
// -x objective-c 是必需的：Cocoa/Foundation 的头文件里全是 Objective-C 语法
// （@class、@interface），cgo 默认按 C 编译会直接报 "expected identifier"。
// Wails 自己的 webview_window_darwin.go 也是这么写的。
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

// 把 child 挂成 parent 的子窗口。
//
// 这是 AppKit 原生机制：子窗口会跟着父窗口移动（原子操作，不经过应用层，
// 所以零延迟、不"飘"），会自动叠在父窗口之上，父窗口最小化时一起最小化，
// 父窗口关闭时一起关掉。Qt 的父子窗口用的就是同一套。
//
// 对比 Wails 自己的 Window.AttachModal：那个走的是 [parent beginSheet:child]，
// 呈现成从标题栏滑下来的 sheet 面板 —— 系统不画红绿灯，尺寸也受限。我们要的
// 是「普通窗口 + 系统级父子关系」，只有 addChildWindow 能给。
static void w3bAttachChild(void *parentPtr, void *childPtr) {
	if (parentPtr == NULL || childPtr == NULL) {
		return;
	}
	NSWindow *parent = (NSWindow *)parentPtr;
	NSWindow *child = (NSWindow *)childPtr;

	// 必须在主线程操作 UI
	dispatch_async(dispatch_get_main_queue(), ^{
		// ordered:NSWindowAbove 保证子窗口叠在父窗口上方
		[parent addChildWindow:child ordered:NSWindowAbove];
	});
}

static void w3bDetachChild(void *parentPtr, void *childPtr) {
	if (parentPtr == NULL || childPtr == NULL) {
		return;
	}
	NSWindow *parent = (NSWindow *)parentPtr;
	NSWindow *child = (NSWindow *)childPtr;

	dispatch_async(dispatch_get_main_queue(), ^{
		[parent removeChildWindow:child];
	});
}
*/
import "C"

import "unsafe"

// attachChildWindow 建立原生父子窗口关系。
func attachChildWindow(parent, child unsafe.Pointer) {
	C.w3bAttachChild(parent, child)
}

// detachChildWindow 解除父子关系。子窗口关闭前调用，避免父窗口持有野指针。
func detachChildWindow(parent, child unsafe.Pointer) {
	C.w3bDetachChild(parent, child)
}
