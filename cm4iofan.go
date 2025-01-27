package cm4iofan

import (
	"errors"
	"math"

	"github.com/go-daq/smbus"
)

const (
	// Emc2301ProductIdReg Register containing the EMC2301's product id (emc2301.pdf: TABLE 6-1)
	Emc2301ProductIdReg = 0xFD
	// Emc2301ProductIdVal The EMC2301's product id (emc2301.pdf: TABLE 6-1)
	Emc2301ProductIdVal = 0x37

	// Emc2301ConfigReg The EMC2301's fan configuration register (emc2301.pdf: 4.1 Fan Control Modes of Operation, 6.14 Fan Configuration Registers)
	Emc2301ConfigReg = 0x32

	// Emc2301DutyCycleReg The EMC2301's fan drive setting register (emc2301.pdf: 6.12 Fan Drive Setting Register)
	Emc2301DutyCycleReg = 0x30

	// Emc2301TachHighReg Register containing the TACH measurement's HIGH byte (emc2301.pdf: 6.23 TACH Reading Registers)
	Emc2301TachHighReg = 0x3E
	// Emc2301TachLowReg Register containing the TACH measurement's LOW byte (emc2301.pdf: 6.23 TACH Reading Registers)
	Emc2301TachLowReg = 0x3F

	// Emc2301Tach2RPM RPM conversion constant (emc2301.pdf: EQUATION 4-3: SIMPLIFIED TACH CONVERSION)
	Emc2301Tach2RPM = 3932160
)

type EMC2301 struct {
	conn *smbus.Conn
}

// New opens the connection to the EMC2301, verifies the product id, performs the initial configuration and returns the handle
func New() (*EMC2301, error) {
	addr, err := FindEmc2301Address()
	if err != nil {
		return nil, err
	}

	c, err := smbus.Open(addr, Emc2301ProductIdReg)
	if err != nil {
		return nil, err
	}

	id, err := c.ReadReg(Emc2301ProductIdReg, 1)
	if err != nil {
		c.Close()
		return nil, err
	}

	if Emc2301ProductIdVal != id[0] {
		c.Close()
		return nil, errors.New("unexpected product id")
	}

	conf, err := c.ReadReg(Emc2301ConfigReg, 1)
	if err != nil {
		c.Close()
		return nil, err
	}

	// set RNG1[1:0] to 500 RPM (-> m = 1)
	conf[0] &= ^(uint8(0b11) << 5)
	err = c.WriteReg(Emc2301ConfigReg, conf)
	if err != nil {
		c.Close()
		return nil, err
	}

	return &EMC2301{conn: c}, nil
}

// FindEmc2301Address finds the I2C bus number where the EMC2301 device is connected
func FindEmc2301Address() (int, error) {
	for bus := 0; bus <= 10; bus++ {
		c, err := smbus.Open(bus, 0x00)
		if err == nil {
			_, err = c.ReadReg(Emc2301ProductIdReg, 1)
			c.Close()
			if err == nil {
				return bus, nil
			}
		}
	}

	return -1, errors.New("device not found")
}

// GetDutyCycle reads and returns the current PWM duty cycle in %
func (ctrl *EMC2301) GetDutyCycle() (int, error) {
	v, err := ctrl.conn.ReadReg(Emc2301DutyCycleReg, 1)
	if err != nil {
		return -1, err
	}
	// emc2301.pdf: EQUATION 4-1: REGISTER VALUE TO DRIVE
	return int(math.Round((float64(v[0]) / 255) * 100)), nil
}

// SetDutyCycle sets the PWM duty cycle in %
func (ctrl *EMC2301) SetDutyCycle(pct int) error {
	if pct < 0 || pct > 100 {
		return errors.New("expecting a value of 0 <= pct <= 100")
	}
	// emc2301.pdf: EQUATION 4-1: REGISTER VALUE TO DRIVE
	v := math.Round(255 * (float64(pct) / 100))
	return ctrl.conn.WriteReg(Emc2301DutyCycleReg, []uint8{uint8(v)})
}

// RPMResult contains the result of a RPM measurement
type RPMResult struct {
	// Rpm The measured/calculated RPM (valid if !Stopped && !Undef)
	Rpm int
	// Stopped is true, when the PWM duty cycle is set to 0
	Stopped bool
	// Undef is true, when the TACH value is too low to calculate the RPM (emc2301.pdf: EQUATION 4-3: SIMPLIFIED TACH CONVERSION)
	Undef bool
}

// GetRPM measures/calculates the current RPM (if possible)
func (ctrl *EMC2301) GetRPM() (*RPMResult, error) {
	h, err := ctrl.conn.ReadReg(Emc2301TachHighReg, 1)
	if err != nil {
		return &RPMResult{}, err
	}
	l, err := ctrl.conn.ReadReg(Emc2301TachLowReg, 1)
	if err != nil {
		return &RPMResult{}, err
	}
	// HIGH BYTE - Bit 7: 2048 ... Bit 0: 32
	// LOW BYTE - Bit 7: 16 ... Bit 3: 1, Bit 0-2: ignored
	tach := uint16(h[0]) << 5
	tach |= uint16(l[0]) >> 3
	// emc2301.pdf: EQUATION 4-3: SIMPLIFIED TACH CONVERSION (with m = 1)
	rpm := int(Emc2301Tach2RPM / uint32(tach))
	// when rpm > 500, we can trust the value
	if rpm > 500 {
		return &RPMResult{Rpm: rpm}, nil
	}
	// check duty cycle to find out if the fan is stopped
	dc, err := ctrl.GetDutyCycle()
	if err != nil {
		return &RPMResult{}, err
	}
	if dc == 0 {
		return &RPMResult{Stopped: true}, nil
	}
	// we can't determine the rpm
	return &RPMResult{Undef: true}, nil
}
