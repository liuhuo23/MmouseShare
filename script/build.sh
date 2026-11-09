#!/usr/bin/env bash
# Build the macOS app with the local signing identity.
#
# The ad hoc signature MyGo uses by default is fine to run the app, but macOS
# remembers a permission (the accessibility one, which the sharing needs) by
# the identity of the program. With an ad hoc signature that identity is the
# hash of the build, so every rebuild asks for the permission again. A
# certificate gives the app one identity for good: grant it once.
#
# script/darwin-signing.sh makes the certificate once. Everything after that is
# this script.
set -euo pipefail

identity="${MMOUSE_SIGN_IDENTITY:-MmouseShare Local Signing}"
keychain="${MMOUSE_SIGN_KEYCHAIN:-$HOME/.mmouse-signing/mmouse-signing.keychain-db}"
# darwin-signing.sh keeps the password next to the keychain it made.
password_file="$(dirname "$keychain")/pw.txt"

if [[ ! -f $keychain ]]; then
	echo "script/build.sh: run script/darwin-signing.sh once first" >&2
	exit 1
fi

# The keychain is unlocked for codesign; it locks when the computer restarts.
security unlock-keychain -p "$(cat "$password_file")" "$keychain"

cd "$(dirname "$0")/.."
go tool mygo build -skip-dmg -sign "$identity" "$@"
