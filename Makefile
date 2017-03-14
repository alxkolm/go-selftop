install:
	sudo cp selftop /usr/local/bin
	mkdir ~/.config/systemd/user --parents --verbose
	cp selftop.service ~/.config/systemd/user
	systemctl --user enable selftop.service
	systemctl --user start selftop.service