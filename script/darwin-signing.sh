#!/usr/bin/env bash
# Make the certificate that signs the app, once.
#
# Run this once on a Mac. It keeps the certificate and its key in a keychain of
# its own, in $HOME/.mmouse-signing, so that the login keychain is untouched and
# codesign needs no password at build time. script/build.sh uses the result;
# the certificate is local to this computer and nothing about it is committed.
#
# To start over, delete $HOME/.mmouse-signing and run this again, then grant the
# accessibility permission in the settings of macOS once more.
set -euo pipefail

name="${MMOUSE_SIGN_IDENTITY:-MmouseShare Local Signing}"
dir="$HOME/.mmouse-signing"
keychain="${MMOUSE_SIGN_KEYCHAIN:-$dir/mmouse-signing.keychain-db}"
# The key, the certificate and the password stay next to the keychain, so that
# another location is another identity, whole.
dir="$(dirname "$keychain")"
password_file="$dir/pw.txt"

if [[ -f $keychain ]]; then
	echo "script/darwin-signing.sh: $keychain is there already, nothing to do"
	exit 0
fi

mkdir -p "$dir"
cd "$dir"

# A certificate of its own, good for ten years, that signs code and nothing
# else: no server it names, no other use the system would trust it for.
openssl req -x509 -newkey rsa:2048 -nodes -keyout key.pem -out cert.pem -days 3650 \
	-subj "/CN=$name/O=Local/C=CN" \
	-addext "keyUsage=critical,digitalSignature" \
	-addext "extendedKeyUsage=critical,codeSigning" \
	-addext "basicConstraints=critical,CA:false" 2>/dev/null

# -legacy keeps the archive in the older form that the keychain of macOS reads.
openssl rand -base64 32 | tr -d '/+=' | cut -c1-24 >"$password_file"
chmod 600 "$password_file"
password="$(cat "$password_file")"
openssl pkcs12 -export -legacy -inkey key.pem -in cert.pem -out sign.p12 \
	-name "$name" -passout pass:"$password"

security create-keychain -p "$password" "$keychain"
security set-keychain-settings "$keychain"
security unlock-keychain -p "$password" "$keychain"
security import sign.p12 -k "$keychain" -P "$password" \
	-T /usr/bin/codesign -T /usr/bin/security
# Let codesign use the key without a prompt.
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$password" \
	"$keychain" >/dev/null

# Look the identity up along with the keychains the person had, and not instead
# of them.
listed="$(security list-keychains -d user | sed -e 's/^[[:space:]]*//' -e 's/"//g')"
# shellcheck disable=SC2086
security list-keychains -d user -s $listed "$keychain"

rm -f sign.p12 key.pem

cat <<EOF
made the certificate "$name"

next: script/build.sh, then start the app, press 启动共享, and allow it once in
      系统设置 → 隐私与安全性 → 辅助功能. The permission stays from then on.
EOF
