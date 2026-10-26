package local

import (
	"github.com/LeXwDeX/Plinth/internal/assets/printreport"
	"os"
)

func inspectPrintReport(file *os.File, size int64) error {
	if size <= 0 || size > printreport.MaxBytes {
		return printreport.ErrInvalid
	}
	return printreport.ValidateReader(file, printreport.MaxBytes)
}
