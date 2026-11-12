# Find the current serial port
ls -l /dev/ttyACM* /dev/ttyUSB* 2>/dev/null
#Assuming it appears as /dev/ttyACM0, inspect its USB identifiers
udevadm info --attribute-walk --name=/dev/ttyACM0
#Expecting these values
#ATTRS{idVendor}=="0483"
#ATTRS{idProduct}=="5740"
#ATTRS{serial}=="314"

#install rules
sudo install -m 0644 deploy/99-terminalfa.rules /etc/udev/rules.d/99-terminalfa.rules

#reload rules
sudo udevadm control --reload-rules
