curl -sS http://127.0.0.1:8090/api/v1/commands \
	-H 'Content-Type: application/json' \
	-d '{"deviceId":"cashdesk-1","cmd":"ID","params":{}}'

curl -sS http://127.0.0.1:8090/healthz \
	-H 'Content-Type: application/json'

curl -sS http://127.0.0.1:8090/api/v1/devices \
	-H 'Content-Type: application/json'

curl -sS http://127.0.0.1:8090/api/v1/commands \
	-H 'Content-Type: application/json'

curl -sS http://127.0.0.1:8090/api/v1/commands \
	-H 'Content-Type: application/json' \
	-d '{"deviceId":"cashdesk-1","cmd":"getFirmwareVersion","params":{}}'

curl -sS http://127.0.0.1:8090/api/v1/commands \
	-H 'Content-Type: application/json' \
	-d '{"deviceId":"cashdesk-1","cmd":"getModel","params":{}}'

curl -sS http://127.0.0.1:8090/api/v1/commands \
	-H 'Content-Type: application/json' \
	-d '{"deviceId":"cashdesk-1","cmd":"getFiscalStorageSerialNumber","params":{}}'

curl -sS http://127.0.0.1:8090/api/v1/commands \
	-H 'Content-Type: application/json' \
	-d '{"deviceId":"cashdesk-1","cmd":"syncDateTime","params":{}}'

