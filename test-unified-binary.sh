#!/usr/bin/env bash
# Integration test for unified forge binary

set -euo pipefail

echo "Building forge binary..."
go build -o forge ./cmd/forge

echo ""
echo "Testing help command..."
./forge help | grep -q "forge — async coding agent" || { echo "FAIL: help missing header"; exit 1; }
./forge help | grep -q "forge stats" || { echo "FAIL: help missing stats command"; exit 1; }

echo ""
echo "Testing stats command..."
output=$(./forge stats)
echo "$output" | grep -q "Monthly Stats" || { echo "FAIL: stats missing header"; exit 1; }
echo "$output" | grep -q "Total:" || { echo "FAIL: stats missing total"; exit 1; }

echo ""
echo "Testing stats --help..."
./forge stats --help 2>&1 | grep -q "\-month" || { echo "FAIL: stats help missing --month flag"; exit 1; }
./forge stats --help 2>&1 | grep -q "\-week" || { echo "FAIL: stats help missing --week flag"; exit 1; }
./forge stats --help 2>&1 | grep -q "\-sessions" || { echo "FAIL: stats help missing --sessions flag"; exit 1; }

echo ""
echo "✅ All integration tests passed!"
echo ""
echo "Available commands:"
echo "  ./forge              # interactive mode"
echo "  ./forge agent        # agent server"
echo "  ./forge server       # gateway server"
echo "  ./forge stats        # cost analytics"
