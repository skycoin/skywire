distclean:
	echo dummy
install:
	mkdir -p $(DESTDIR)/opt/skywire/apps
	mkdir -p $(DESTDIR)/usr/bin
	install -m 0755 skywire-visor $(DESTDIR)/opt/skywire/skywire-visor
	install -m 0755 skywire-cli $(DESTDIR)/opt/skywire/skywire-cli
	install -m 0755 apps/skychat $(DESTDIR)/opt/skywire/apps/skychat
	install -m 0755 apps/skysocks $(DESTDIR)/opt/skywire/apps/skysocks
	install -m 0755 apps/skysocks-client $(DESTDIR)/opt/skywire/apps/skysocks-client
	install -m 0755 apps/vpn-server $(DESTDIR)/opt/skywire/apps/vpn-server
	install -m 0755 apps/vpn-client $(DESTDIR)/opt/skywire/apps/vpn-client
	ln -s /opt/skywire/skywire-visor $(DESTDIR)/usr/bin/skywire-visor
	ln -s /opt/skywire/skywire-cli $(DESTDIR)/usr/bin/skywire-cli

uninstall:
	rm -rf $(DESTDIR)/usr/bin/skywire-visor
	rm -rf $(DESTDIR)/usr/bin/skywire-cli
	rm -rf $(DESTDIR)/opt/skywire
