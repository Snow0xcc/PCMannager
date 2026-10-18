package sysutil

import (
	"os"
	"strconv"
	"testing"
)

// TestAcquireSingleInstance 钉住两平台实现（Windows 互斥量 / Unix flock）
// 的公共语义：首取成功、二取拒绝、释放后可重取。
// 此前两平台都有实现但零调用方——README 宣称的"单实例"从未接线（C1），
// 现由 main.go 接线，本测试防止实现回归。
func TestAcquireSingleInstance(t *testing.T) {
	// 进程内唯一名字，避免与其它测试/真实实例互扰。
	name := "pcmannager-test-" + strconv.Itoa(os.Getpid())

	release, ok := AcquireSingleInstance(name)
	if !ok {
		t.Fatal("首次获取应成功")
	}
	t.Cleanup(release)

	if _, ok := AcquireSingleInstance(name); ok {
		t.Fatal("第二次获取应失败（已有实例持有）")
	}

	release()
	reacquire, ok := AcquireSingleInstance(name)
	if !ok {
		t.Fatal("释放后应可重新获取")
	}
	reacquire()
}
