#!/usr/bin/env bash

if [ "$EUID" -ne 0 ]; then
  echo "$0 should be run as root, exiting..."
  exit
fi

if [ "$(uname -s)" != "Linux" ]; then
  echo "$0 can only be run from Linux host"
fi

  cat <<EOF >>/usr/share/polkit-1/actions/org.freedesktop.policykit.skywire-visor.policy
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE policyconfig PUBLIC
 "-//freedesktop//DTD PolicyKit Policy Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/PolicyKit/1/policyconfig.dtd">
<policyconfig>
    <action id="org.freedesktop.policykit.pkexec.skywire-visor">
    <description>Run skywire-visor program as root</description>
    <message>Authentication is required to run skywire-visor GUI</message>
    <icon_name>nm-icon</icon_name>
    <defaults>
        <allow_any>auth_admin</allow_any>
        <allow_inactive>auth_admin</allow_inactive>
        <allow_active>auth_admin</allow_active>
    </defaults>
    <annotate key="org.freedesktop.policykit.exec.path">${1}</annotate>
    <annotate key="org.freedesktop.policykit.exec.allow_gui">true</annotate>
    </action>
</policyconfig>
EOF
