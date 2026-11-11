//go:build windows

package main

import winplatform "github.com/Endergarten/mc-server-manager/internal/platform/windows"

func platformJavaRoots() []string { return winplatform.JavaRoots() }
func platformDefaultJava() string { return winplatform.DefaultJava() }
