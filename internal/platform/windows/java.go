//go:build windows

package windows

import (
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
)

func DefaultJava() string {
	p := filepath.Join(os.Getenv("APPDATA"), ".minecraft", "runtime", "java-runtime-epsilon", "bin", "java.exe")
	if _, e := os.Stat(p); e == nil {
		return p
	}
	return ""
}

func JavaRoots() []string {
	roots := []string{}
	for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		for _, vendor := range []string{"Java", "Eclipse Adoptium", "Microsoft", "Zulu", "BellSoft", "Amazon Corretto", "Semeru", "Programs"} {
			roots = append(roots, filepath.Join(base, vendor))
		}
	}
	home, _ := os.UserHomeDir()
	for _, base := range []string{home, os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")} {
		if base == "" {
			continue
		}
		for _, name := range []string{".minecraft", ".jdks", ".pcl", "PCL", "PCL2", "PrismLauncher", "HMCL", ".hmcl", "Minecraft"} {
			roots = append(roots, filepath.Join(base, name))
		}
	}
	for _, drive := range []string{"C:", "D:", "E:", "F:", "G:"} {
		for _, name := range []string{"Java", "jdk", "jre", "Minecraft", "MC", "PCL", "PCL2", "HMCL", "Games\\Minecraft"} {
			roots = append(roots, filepath.Join(drive+"\\", name))
		}
	}
	for _, hive := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		for _, key := range []string{`SOFTWARE\JavaSoft\JDK`, `SOFTWARE\JavaSoft\Java Development Kit`, `SOFTWARE\JavaSoft\Java Runtime Environment`, `SOFTWARE\JavaSoft\JRE`} {
			k, e := registry.OpenKey(hive, key, registry.READ)
			if e != nil {
				continue
			}
			names, _ := k.ReadSubKeyNames(-1)
			for _, name := range names {
				sub, e := registry.OpenKey(k, name, registry.READ)
				if e != nil {
					continue
				}
				p, _, e := sub.GetStringValue("JavaHome")
				sub.Close()
				if e == nil && p != "" {
					roots = append(roots, p)
				}
			}
			k.Close()
		}
	}
	return roots
}
