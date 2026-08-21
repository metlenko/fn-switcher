BINARY_NAME=fn-switcher
APP_NAME=FnSwitcher.app
APP_CONTENTS=$(APP_NAME)/Contents
APP_MACOS=$(APP_CONTENTS)/MacOS
INSTALL_PATH=/usr/local/bin
INSTALL_APP_PATH=/Applications/$(APP_NAME)
PLIST_NAME=com.user.fnswitcher.plist
PLIST_PATH=~/Library/LaunchAgents/$(PLIST_NAME)

.PHONY: build app install uninstall install-agent uninstall-agent clean status restart

build:
	go build -o $(BINARY_NAME)

app: build
	@mkdir -p $(APP_MACOS)
	@cp $(BINARY_NAME) $(APP_MACOS)/
	@echo '<?xml version="1.0" encoding="UTF-8"?>' > $(APP_CONTENTS)/Info.plist
	@echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' >> $(APP_CONTENTS)/Info.plist
	@echo '<plist version="1.0">' >> $(APP_CONTENTS)/Info.plist
	@echo '<dict>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <key>CFBundleIdentifier</key>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <string>com.user.fnswitcher</string>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <key>CFBundleName</key>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <string>FnSwitcher</string>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <key>CFBundleDisplayName</key>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <string>Fn Switcher</string>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <key>CFBundleExecutable</key>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <string>fn-switcher</string>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <key>CFBundlePackageType</key>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <string>APPL</string>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <key>LSUIElement</key>' >> $(APP_CONTENTS)/Info.plist
	@echo '    <true/>' >> $(APP_CONTENTS)/Info.plist
	@echo '</dict>' >> $(APP_CONTENTS)/Info.plist
	@echo '</plist>' >> $(APP_CONTENTS)/Info.plist
	@echo "Created $(APP_NAME)"

install: app
	sudo cp -R $(APP_NAME) /Applications/
	sudo ln -sf $(INSTALL_APP_PATH)/Contents/MacOS/$(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Installed to $(INSTALL_APP_PATH)"
	@echo "CLI symlink: $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo ""
	@echo "Next steps:"
	@echo "1. Add $(INSTALL_APP_PATH) to System Settings → Privacy & Security → Accessibility"
	@echo "2. Set 'Press 🌐 key to' → 'Do Nothing' in System Settings → Keyboard"
	@echo "3. Run: fn-switcher"
	@echo ""
	@echo "For autostart run: make install-agent"

uninstall: uninstall-agent
	sudo rm -rf $(INSTALL_APP_PATH)
	sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "Uninstalled"

install-agent:
	@mkdir -p ~/Library/LaunchAgents
	@echo '<?xml version="1.0" encoding="UTF-8"?>' > $(PLIST_PATH)
	@echo '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">' >> $(PLIST_PATH)
	@echo '<plist version="1.0">' >> $(PLIST_PATH)
	@echo '<dict>' >> $(PLIST_PATH)
	@echo '    <key>Label</key>' >> $(PLIST_PATH)
	@echo '    <string>com.user.fnswitcher</string>' >> $(PLIST_PATH)
	@echo '    <key>ProgramArguments</key>' >> $(PLIST_PATH)
	@echo '    <array>' >> $(PLIST_PATH)
	@echo '        <string>$(INSTALL_APP_PATH)/Contents/MacOS/$(BINARY_NAME)</string>' >> $(PLIST_PATH)
	@echo '    </array>' >> $(PLIST_PATH)
	@echo '    <key>RunAtLoad</key>' >> $(PLIST_PATH)
	@echo '    <true/>' >> $(PLIST_PATH)
	@echo '    <key>KeepAlive</key>' >> $(PLIST_PATH)
	@echo '    <true/>' >> $(PLIST_PATH)
	@echo '</dict>' >> $(PLIST_PATH)
	@echo '</plist>' >> $(PLIST_PATH)
	launchctl load $(PLIST_PATH)
	@echo "LaunchAgent installed and loaded"

uninstall-agent:
	launchctl unload $(PLIST_PATH) 2>/dev/null
	rm -f $(PLIST_PATH)
	@echo "LaunchAgent removed"

clean:
	rm -f $(BINARY_NAME)
	rm -rf $(APP_NAME)

status:
	@launchctl list | grep fnswitcher || echo "Not running"

restart: uninstall-agent install-agent
