#!/bin/bash

set -e

echo "Building New API Electron App..."
VERSION="$(git describe --tags --always)"

verify_linked_version() {
    local binary_path="$1"
    local observed_version
    observed_version="$(
        cd electron &&
        test ! -e .env &&
        env -u VERSION "$binary_path" --version
    )"
    test "$observed_version" = "$VERSION"
}

echo "Step 1: Building frontend..."
cd ../web
bun install --frozen-lockfile
DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION="$VERSION" bun run build
bun run release:verify-version -- "$VERSION"
cd ../electron

echo "Step 2: Building Go backend..."
cd ..
REPOSITORY_ROOT="$PWD"

if [[ "$OSTYPE" == "darwin"* ]]; then
    echo "Building for macOS..."
    CGO_ENABLED=1 go build -ldflags="-s -w -X=github.com/QuantumNous/new-api/common.Version=${VERSION}" -o new-api .
    verify_linked_version "$REPOSITORY_ROOT/new-api"
    cd electron
    npm install
    npm run build:mac
elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
    echo "Building for Linux..."
    CGO_ENABLED=1 go build -ldflags="-s -w -X=github.com/QuantumNous/new-api/common.Version=${VERSION}" -o new-api .
    verify_linked_version "$REPOSITORY_ROOT/new-api"
    cd electron
    npm install
    npm run build:linux
elif [[ "$OSTYPE" == "msys" || "$OSTYPE" == "cygwin" || "$OSTYPE" == "win32" ]]; then
    echo "Building for Windows..."
    CGO_ENABLED=1 go build -ldflags="-s -w -X=github.com/QuantumNous/new-api/common.Version=${VERSION}" -o new-api.exe .
    verify_linked_version "$REPOSITORY_ROOT/new-api.exe"
    cd electron
    npm install
    npm run build:win
else
    echo "Unknown OS, building for current platform..."
    CGO_ENABLED=1 go build -ldflags="-s -w -X=github.com/QuantumNous/new-api/common.Version=${VERSION}" -o new-api .
    verify_linked_version "$REPOSITORY_ROOT/new-api"
    cd electron
    npm install
    npm run build
fi

echo "Build complete! Check electron/dist/ for output."
