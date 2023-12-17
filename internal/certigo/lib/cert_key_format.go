package lib

import (
	"strings"
)

func NewCertificateKeyFormat(fileFormat string) CertificateKeyFormat {
	fileFormat = strings.ToUpper(fileFormat)
	switch fileFormat {
	case "":
		return CertKeyFormatNONE
	case "PEM":
		return CertKeyFormatPEM
	case "DER":
		return CertKeyFormatDER
	case "JCEKS":
		return CertKeyFormatJCEKS
	case "PKCS12", "P12":
		return CertKeyFormatPKCS12
	default:
		return CertKeyFormatNONE
	}
}

type CertificateKeyFormat string

const (
	CertKeyFormatNONE CertificateKeyFormat = ""
	// The file contains plain-text PEM data
	CertKeyFormatPEM CertificateKeyFormat = "PEM"
	// The file contains X.509 DER encoded data
	CertKeyFormatDER CertificateKeyFormat = "DER"
	// The file contains JCEKS keystores
	CertKeyFormatJCEKS CertificateKeyFormat = "JCEKS"
	// The file contains PFX data describing PKCS#12
	CertKeyFormatPKCS12 CertificateKeyFormat = "PKCS12"
)

func (f *CertificateKeyFormat) Set(fileFormat string) {
	*f = NewCertificateKeyFormat(fileFormat)
}

func (f CertificateKeyFormat) IsNone() bool {
	return f == CertKeyFormatNONE
}
