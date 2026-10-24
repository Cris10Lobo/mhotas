.PHONY: server fake-phone

# Built as Windows .exe files and run through WSL2 interop so they get real
# DLL access (SimConnect, vJoy). Use INPUT=vjoy|simconnect to skip the TUI picker.
server:
	cd server/cmd/mhotas-server && GOOS=windows GOARCH=amd64 go build -o mhotas-server.exe . && ./mhotas-server.exe --backend=simconnect --config=../../config.json $(if $(INPUT),--input=$(INPUT))

# A Linux process would sit in a different network namespace than the
# Windows server, and WSL2 UDP loopback forwarding is unreliable.
fake-phone:
	cd tools/udp-fake-phone && GOOS=windows GOARCH=amd64 go build -o udp-fake-phone.exe . && ./udp-fake-phone.exe --target=127.0.0.1:9750
