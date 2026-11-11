//go:build !windows

package main

func platformDefaultJava() string { return "" }

func platformJavaRoots() []string {
	return []string{"/usr/lib/jvm", "/usr/java", "/opt/java", "/Library/Java/JavaVirtualMachines"}
}
