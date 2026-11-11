# Desktop design contract — preview pending approval

Target: Windows 10/11, Go/Wails, personal Minecraft map launcher. Standard desktop density, mouse and drag/drop first, keyboard accessible. Review viewport 1280×820, minimum supported window 900×650.

Evidence: existing `native-layout.png` screenshot, current frontend, the user's corrections and Microsoft Fluent guidance. No visual draft is being treated as an implementation screenshot.

Keep maps and their saved progress as the primary objects. Show a readable map list and a selected-map detail pane with image, description and one launch action. Console is a separate tab. Version requirements are secondary metadata. Configurations and administrator names are global settings, never repeated per map.

Audit drivers: former 10–12 px text was too small; the table-first interface looked like an administration panel; status/footer copy added noise; a large administrator textarea made a simple list hard to manage.

Redesign: Windows system font, 14 px controls, 12–13 px metadata, 16 px map names, 22 px page title; 4/8 px spacing rhythm, 72–84 px map rows, restrained Fluent blue accent. Use a source list rather than a table when there are few comparable fields. Use real window controls in the desktop host. No fake title bar, marketing slogans, permanent bottom status strip, desktop-mode caption or exit shortcut.

Navigation: maps and version downloads first; Java and settings together in a second group. Map launch/stop belongs beside the selected map. Editing, backup and deletion are secondary commands. Deletion requires confirmation and is disabled while the map is running.

Settings: Java auto/manual mode and global memory/port. Only three server switches are exposed: authentication (off by default), command blocks (on), flight allowed (on). Whitelist is always disabled and hidden. Administrators use individual rows plus an add field and per-row remove action. Settings changes are saved explicitly. Map import offers version suggestions and begins downloading a missing selected server version after confirmation.

States: distinguish hover, persistent selection, keyboard focus, disabled commands, preparing/downloading, running, stopping and errors. Keep errors near the related task where practical. Support light/dark, forced colors and reduced motion. Native lifecycle and file picker are handled by the desktop module.

Validation: review map/settings/console screenshots; test map selection, tabs, administrator add/remove/duplicate rejection, import/download request sequence, deletion confirmation and blocked running deletion. Check 0/1/10/100 maps, long names, 900×650 and keyboard navigation. Browser preview verifies shared content only, not native window/dialog behavior or Windows 10 hardware compatibility.

Release gate: the user approved the current visual direction with the three global switch changes. Package and push only after their verification.
