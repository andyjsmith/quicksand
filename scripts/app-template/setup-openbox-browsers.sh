#!/bin/sh
# Turns an Ubuntu (26.04) VM with the Quicksand agent into a lightweight
# browser template: LightDM signs the user straight in to a bare Openbox
# session instead of GNOME, with Chrome and Firefox installed for Quicksand
# to launch. See docs/app-templates.md.
#
# Usage, as root in the guest, with quicksand-follow-display next to it:
#   sh setup-openbox-browsers.sh <user to sign in>
set -eux
USER_NAME=${1:?usage: setup-openbox-browsers.sh <user to sign in>}
HERE=$(dirname "$0")
export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y --no-install-recommends \
  lightdm lightdm-gtk-greeter openbox python3-xdg xserver-xorg xserver-xorg-video-qxl x11-xserver-utils \
  xclip spice-vdagent wget ca-certificates gnupg fonts-dejavu-core fonts-liberation

# Firefox from Mozilla's APT repository rather than the snap, which starts slowly.
install -d -m 0755 /etc/apt/keyrings
wget -qO /etc/apt/keyrings/packages.mozilla.org.asc https://packages.mozilla.org/apt/repo-signing-key.gpg
echo "deb [signed-by=/etc/apt/keyrings/packages.mozilla.org.asc] https://packages.mozilla.org/apt mozilla main" \
  > /etc/apt/sources.list.d/mozilla.list
printf 'Package: *\nPin: origin packages.mozilla.org\nPin-Priority: 1000\n' > /etc/apt/preferences.d/mozilla
snap remove --purge firefox || true
apt-get update
# Ubuntu's snap stub has a higher version than Mozilla's package.
apt-get install -y --allow-downgrades firefox

# Chrome from Google.
wget -qO /tmp/chrome.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb
apt-get install -y /tmp/chrome.deb
rm /tmp/chrome.deb

# No first-run screens, default-browser prompts or promotions.
install -d /etc/opt/chrome/policies/managed /etc/firefox/policies
cat > /etc/opt/chrome/policies/managed/quicksand.json <<'J'
{
  "DefaultBrowserSettingEnabled": false,
  "PromotionalTabsEnabled": false,
  "MetricsReportingEnabled": false,
  "PrivacySandboxPromptEnabled": false,
  "BrowserSignin": 0,
  "SyncDisabled": true,
  "PasswordManagerEnabled": false
}
J
cat > /etc/firefox/policies/policies.json <<'J'
{
  "policies": {
    "DontCheckDefaultBrowser": true,
    "DisableTelemetry": true,
    "SkipTermsOfUse": true,
    "OverrideFirstRunPage": "",
    "OverridePostUpdatePage": "",
    "UserMessaging": { "WhatsNew": false, "ExtensionRecommendations": false, "FeatureRecommendations": false, "SkipOnboarding": true, "MoreFromMozilla": false, "FirefoxLabs": false },
    "NoDefaultBookmarks": true
  }
}
J

# Sign the user straight in to Openbox.
groupadd -rf autologin
gpasswd -a "$USER_NAME" autologin
install -d /etc/lightdm/lightdm.conf.d
cat > /etc/lightdm/lightdm.conf.d/50-quicksand.conf <<C
[Seat:*]
autologin-user=$USER_NAME
autologin-user-timeout=0
autologin-session=openbox
user-session=openbox
C
# Windows open maximized without decorations, and the screen never blanks.
grep -q 'class="\*" type="normal"' /etc/xdg/openbox/rc.xml ||
  sed -i 's|<applications>|<applications>\n  <application class="*" type="normal"><decor>no</decor><maximized>yes</maximized></application>|' /etc/xdg/openbox/rc.xml
install -m 0755 "$HERE/quicksand-follow-display" /usr/local/bin/quicksand-follow-display
cat > /etc/xdg/openbox/autostart <<'A'
xset s off -dpms
xsetroot -solid '#1f2937'
quicksand-follow-display &
A
echo /usr/sbin/lightdm > /etc/X11/default-display-manager
systemctl disable gdm3 2>/dev/null || systemctl disable gdm || true
ln -sf /usr/lib/systemd/system/lightdm.service /etc/systemd/system/display-manager.service
systemctl set-default graphical.target

# Faster boots (see "Faster startup" in docs/app-templates.md). Ubuntu's GRUB
# shows its menu for 30 seconds after any boot that didn't finish cleanly,
# which throwaway VMs (hard-stopped, reset, cloned) hit all the time.
cat > /etc/default/grub.d/90-quicksand.cfg <<'G'
GRUB_TIMEOUT=0
GRUB_TIMEOUT_STYLE=hidden
GRUB_RECORDFAIL_TIMEOUT=0
G
update-grub
grub-editenv /boot/grub/grubenv unset recordfail
# Nothing here needs snaps any more (Firefox and Chrome are .debs), and
# snapd sits on the boot's critical path.
apt-get purge -y snapd
rm -rf /snap /var/snap /var/lib/snapd "/home/$USER_NAME/snap"
echo done
