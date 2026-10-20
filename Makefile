.PHONY: server fake-phone

# Cross-compiles the real mhotas-server against the real SimConnect backend
# (input mode: TUI picker, or pass INPUT=vjoy|simconnect)
# and runs it via WSL2's interop, which executes .exe files as real Windows
# processes with real DLL access — no need to switch to a Windows shell.
# Pair with `make fake-phone` in another terminal to exercise it without a
# phone.
server:
	cd server/cmd/mhotas-server && GOOS=windows GOARCH=amd64 go build -o mhotas-server.exe . && ./mhotas-server.exe --backend=simconnect --config=../../config.json $(if $(INPUT),--input=$(INPUT))

# Cross-compiled to a real Windows .exe too, not `go run` — running it as a
# plain WSL/Linux process would put it on a different network namespace
# than mhotas-server.exe (a real Windows process via interop), and WSL2's
# loopback forwarding for UDP isn't reliable enough to bridge that. Both
# sides as real Windows processes means they share the actual Windows
# loopback directly, no WSL networking involved.
fake-phone:
	cd tools/udp-fake-phone && GOOS=windows GOARCH=amd64 go build -o udp-fake-phone.exe . && ./udp-fake-phone.exe --target=127.0.0.1:9750
