package exporter

import (
	"crypto/x509"
	"encoding/pem"
	"github.com/golang/glog"
	"github.com/prometheus/client_golang/prometheus"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var extensions = []string{".pem", ".crt", ".cert", ".cer"}

func findCertPaths(p string, exPaths []string) ([]string, error) {
	paths := []string{}
	err := filepath.Walk(p, func(path string, info os.FileInfo, err error) error {

		if len(exPaths) > 0 {
			for _, exPath := range exPaths {
				if strings.Contains(filepath.Dir(path), exPath) || path == exPath {
					return nil
				}
			}
		}

		if err != nil {
			return err
		}
		if isCertFile(path) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func isCertFile(p string) bool {
	for _, e := range extensions {
		if filepath.Ext(p) == e {
			return true
		}
	}
	return false
}

// Exporter implements prometheus.Collector interface
type Exporter struct {
	mux        sync.Mutex
	roots      []string
	exRoots    []string
	certExpiry *prometheus.GaugeVec
}

// SetRoots sets the list of file paths that the exporter should search for certificates in
func (e *Exporter) SetRoots(p []string) {
	e.roots = p
}

// SetExcludeRoots sets the list of file paths that the exporter should exclude search for certificates in
func (e *Exporter) SetExcludeRoots(p []string) {
	e.exRoots = p
}

// Collect satisfies prometheus.Collector interface
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mux.Lock()
	defer e.mux.Unlock()
	e.Scrape(ch)
}

// Describe satisfies prometheus.Collector interface
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.certExpiry.WithLabelValues("path", "issuer", "alg", "version", "subject", "dns_names", "email_addresses").Desc()
}

// Scrape iterates over the list of file paths (set by SetRoot) and parses any found x509 certificates.
// Certificates are parsed and the fields are mapped to prometheus labels which attached to a Gauge.
// Scrape will create a new time series for each certificate file with its associated labels. The value
// of the series equals the expiry of the certificate in seconds.
func (e *Exporter) Scrape(ch chan<- prometheus.Metric) {
	for _, root := range e.roots {
		paths, err := findCertPaths(root, e.exRoots)
		if err != nil {
			glog.Warningf("Error looking for certificates in %s: %s", root, err)
			continue
		}
		for _, path := range paths {

			data, err := ioutil.ReadFile(path)
			if err != nil {
				glog.Warningf("Couldn't read %s: %s", path, err.Error())
				continue
			}

			block, _ := pem.Decode(data)
			if block == nil {
				glog.Warningf("Couldn't decode %s: %s", path, err.Error())
				continue
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				glog.Warningf("Couldn't parse %s: %s", path, err.Error())
				continue
			}

			labels := prometheus.Labels{
				"path":            path,
				"issuer":          cert.Issuer.String(),
				"alg":             cert.SignatureAlgorithm.String(),
				"version":         strconv.Itoa(cert.Version),
				"subject":         cert.Subject.String(),
				"dns_names":       strings.Join(cert.DNSNames, ","),
				"email_addresses": strings.Join(cert.EmailAddresses, ","),
			}

			since := time.Until(cert.NotAfter)
			e.certExpiry.With(labels).Set(since.Seconds())
			ch <- e.certExpiry.With(labels)
		}
	}

}

// New creates an instance of Exporter and returns it
func New() *Exporter {
	return &Exporter{
		certExpiry: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "ssl_certificate",
			Subsystem: "expiry",
			Name:      "seconds",
			Help:      "Number of seconds until certificate expires",
		},
			[]string{"path", "issuer", "alg", "version", "subject", "dns_names", "email_addresses"}),
	}
}
