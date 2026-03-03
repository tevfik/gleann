#!/bin/bash
set -e

# Bu script llama-server binary'lerini indirip embed edilmeye hazir hale getirir.
# Sadece gelistirme amaciyla build asamasindan once bir kez calistirilmalidir.

LLAMA_VERSION="b4898" # Stable olarak tespit ettigimiz bir surum
URL_LINUX="https://github.com/ggerganov/llama.cpp/releases/download/${LLAMA_VERSION}/llama-${LLAMA_VERSION}-bin-ubuntu-x64.zip"

echo "Downloading Linux llama-server..."
wget -q -O linux.zip $URL_LINUX
unzip -q -j linux.zip "build/bin/llama-server" -d bin/
mv bin/llama-server bin/llama-server-linux-amd64
rm linux.zip

# Eger Windows ve Mac binary'lerini de indirmek isterseniz asagidakilerin yorumunu kaldirin:
# echo "Downloading Windows llama-server..."
# wget -q -O win.zip https://github.com/ggerganov/llama.cpp/releases/download/${LLAMA_VERSION}/llama-${LLAMA_VERSION}-bin-win-avx2-x64.zip
# unzip -q -j win.zip "build/bin/llama-server.exe" -d bin/
# mv bin/llama-server.exe bin/llama-server-windows-amd64.exe
# rm win.zip

echo "Download complete! Embeddable binaries are in internal/backend/llamacpp/bin/"
