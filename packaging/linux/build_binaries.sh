#! /bin/bash

set -e -u -o pipefail

here="$(dirname "$BASH_SOURCE")"
this_repo="$(git -C "$here" rev-parse --show-toplevel)"

mode="$("$here/../build_mode.sh" "$@")"
binary_name="$("$here/../binary_name.sh" "$@")"

# Take the second argument as the build root, or a tmp dir if there is no
# second argument.
build_root="${2:-$(mktemp -d)}"

# Record the version now, and write it to the build root. Because it uses a
# timestamp in prerelease mode, it's important that other scripts use this file
# instead of recomputing the version themselves.
version="$("$here/../version.sh" "$@")"
echo -n "$version" > "$build_root/VERSION"
echo -n "$mode" > "$build_root/MODE"

echo "Building version $version $mode in $build_root"

# Determine the Go tags.
go_tags=""
if [ "$mode" = "production" ] ; then
  go_tags="production"
elif [ "$mode" = "prerelease" ] ; then
  go_tags="production prerelease"
elif [ "$mode" = "staging" ] ; then
  go_tags="staging"
fi
echo "-tags '$go_tags'"

# Determine the LD flags.
ldflags=""
if [ "$mode" != "production" ] ; then
  # The non-production build number is everything in the version after the hyphen.
  build_number="$(echo -n "$version" | sed 's/.*-//')"
  ldflags="-X github.com/keybase/client/go/libkb.CustomBuild=$build_number"
fi
echo "-ldflags '$ldflags'"

should_build_kbfs() {
  [ "$mode" != "production" ]
}

# Install the electron dependencies.
if should_build_kbfs ; then
  echo "Installing Node modules for Electron"
  # Can't seem to get the right packages installed under NODE_ENV=production.
  export NODE_ENV=development
  (cd "$here/../../react-native" && npm i)
  (cd "$here/../../desktop" && npm i)
  export NODE_ENV=production
fi

build_one_architecture() {
  layout_dir="$build_root/binaries/$debian_arch"
  mkdir -p "$layout_dir/usr/bin"

  # Always build with vendoring on.
  export GO15VENDOREXPERIMENT=1

  # Assemble a custom GOPATH. Symlinks work for us here, because both the
  # client repo and the kbfs repo are fully vendored.
  export GOPATH="$build_root/gopaths/$debian_arch"
  mkdir -p "$GOPATH/src/github.com/keybase"
  ln -s "$this_repo" "$GOPATH/src/github.com/keybase/client"

  # Build the client binary. Note that `go build` reads $GOARCH.
  echo "Building client for $GOARCH..."
  go build -tags "$go_tags" -ldflags "$ldflags" -o \
    "$layout_dir/usr/bin/$binary_name" github.com/keybase/client/go/keybase

  cp "$here/run_keybase.sh" "$layout_dir/usr/bin/"

  # Short-circuit if we're not building electron.
  if ! should_build_kbfs ; then
    echo "SKIPPING kbfs and electron."
    return
  fi

  # Build the kbfsfuse binary. Currently, this always builds from master.
  echo "Building kbfs for $GOARCH..."
  kbfs_repo="$(dirname "$this_repo")/kbfs"
  # Make sure the kbfs repo is clean and up to date.
  "$here/../check_status_and_pull.sh" "$kbfs_repo"
  ln -s "$kbfs_repo" "$GOPATH/src/github.com/keybase/kbfs"
  go build -tags "$go_tags" -ldflags "$ldflags" -o \
    "$layout_dir/usr/bin/kbfsfuse" github.com/keybase/kbfs/kbfsfuse

  # Build Electron.
  echo "Building Electron client for $electron_arch..."
  (
    cd "$here/../../desktop"
    node package.js --platform linux --arch $electron_arch
    mkdir -p "$layout_dir/opt/keybase"
    rsync -a "release/linux-${electron_arch}/Keybase-linux-${electron_arch}/" \
      "$layout_dir/opt/keybase"
  )
}

export GOARCH=amd64
export debian_arch=amd64
export electron_arch=x64
build_one_architecture

export GOARCH=386
export debian_arch=i386
export electron_arch=ia32
build_one_architecture
