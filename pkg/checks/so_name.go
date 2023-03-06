package checks

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wolfi-dev/wolfictl/pkg/tar"

	"github.com/wolfi-dev/wolfictl/pkg/lint"

	"golang.org/x/exp/maps"

	"github.com/wolfi-dev/wolfictl/pkg/melange"

	"github.com/wolfi-dev/wolfictl/pkg/apk"

	"github.com/pkg/errors"
	"gitlab.alpinelinux.org/alpine/go/repository"
)

type SoNameOptions struct {
	Client              *http.Client
	Logger              *log.Logger
	PackageListFilename string
	Dir                 string
	PackagesDir         string
	PackageNames        []string
	ApkIndexURL         string
}

type NewApkPackage struct {
	Arch    string
	Epoch   string
	Version string
}

func NewSoName() *SoNameOptions {
	o := &SoNameOptions{
		Client: http.DefaultClient,
		Logger: log.New(log.Writer(), "wolfictl check so-name: ", log.LstdFlags|log.Lmsgprefix),
	}

	return o
}

/*
CheckSoName will check if a new APK contains a foo.so file, then compares it with the latest version in an APKINDEX to check
if there are differences.
*/
//nolint:gocritic // hugeParam for entry
func (o SoNameOptions) CheckSoName() error {
	apkContext := apk.New(o.Client, o.ApkIndexURL)
	existingPackages, err := apkContext.GetApkPackages()
	if err != nil {
		return errors.Wrapf(err, "failed to get APK packages from URL %s", o.ApkIndexURL)
	}

	// get a list of new package names that have recently been built
	newPackages, err := o.getNewPackages()
	if err != nil {
		return errors.Wrapf(err, "failed to get new packages")
	}

	soNameErrors := make(lint.EvalRuleErrors, 0)
	// for every new package built lets compare *.so names with the previous released version
	for packageName, newAPK := range newPackages {
		o.Logger.Printf("checking %s", packageName)
		err = o.diff(packageName, newAPK, existingPackages)

		if err != nil {
			soNameErrors = append(soNameErrors, lint.EvalRuleError{
				Error: fmt.Errorf(err.Error()),
			})
		}
	}

	return soNameErrors.WrapErrors()
}

// the wolfi package repo CI will write a file entry for every new .apk package that's been built
// in the form $ARCH|$PACKAGE_NAME|$VERSION_r$EPOCH
//
//nolint:gocritic // hugeParam for entry
func (o SoNameOptions) getNewPackages() (map[string]NewApkPackage, error) {
	rs := make(map[string]NewApkPackage)
	original, err := os.Open(o.PackageListFilename)
	if err != nil {
		return rs, errors.Wrapf(err, "opening file %s", o.PackageListFilename)
	}

	scanner := bufio.NewScanner(original)
	defer original.Close()
	scanner.Split(bufio.ScanLines)

	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		parts := strings.Split(scanner.Text(), "|")

		if len(parts) != 3 {
			return rs, fmt.Errorf("expected 3 parts but found %d when scanning %s", len(parts), scanner.Text())
		}
		versionParts := strings.Split(parts[2], "-")
		if len(versionParts) != 2 {
			return rs, fmt.Errorf("expected 2 version parts but found %d", len(versionParts))
		}

		arch := parts[0]
		packageName := parts[1]
		version := versionParts[0]

		epoch := versionParts[1]
		epoch = strings.TrimPrefix(epoch, "r")
		epoch = strings.TrimSuffix(epoch, ".apk")

		rs[packageName] = NewApkPackage{
			Version: version,
			Epoch:   epoch,
			Arch:    arch,
		}
	}

	rs = o.addSubpackages(rs)
	return rs, nil
}

//nolint:gocritic // hugeParam for entry
func (o SoNameOptions) addSubpackages(m map[string]NewApkPackage) map[string]NewApkPackage {
	packagesAndSubpackages := make(map[string]NewApkPackage)
	maps.Copy(packagesAndSubpackages, m)

	for melangePackageName, apkPackage := range m {
		filename := filepath.Join(o.Dir, melangePackageName+".yaml")
		c, err := melange.ReadMelangeConfig(filename)
		if err != nil {
			log.Printf("failed to read melange config %s", filename)
			continue
		}

		for i := 0; i < len(c.Subpackages); i++ {
			packagesAndSubpackages[c.Subpackages[i].Name] = apkPackage
		}
	}
	return packagesAndSubpackages
}

// diff will compare the so name versions between the latest existing apk in a APKINDEX with a newly built local apk
//
//nolint:gocritic // hugeParam for entry
func (o SoNameOptions) diff(newPackageName string, newAPK NewApkPackage, existingPackages map[string]*repository.Package) error {
	dirExistingApk := os.TempDir()
	dirNewApk := os.TempDir()

	// read new apk
	filename := filepath.Join(o.PackagesDir, newAPK.Arch, fmt.Sprintf("%s-%s-r%s.apk", newPackageName, newAPK.Version, newAPK.Epoch))
	newFile, err := os.Open(filename)
	if err != nil {
		return errors.Wrapf(err, "failed to read %s", filename)
	}

	err = tar.Untar(newFile, dirNewApk)
	if err != nil {
		return errors.Wrapf(err, "failed to untar new apk")
	}

	newSonameFiles, err := o.getSonameFiles(dirNewApk)
	if err != nil {
		return errors.Wrapf(err, "error when looking for soname files in new apk")
	}
	// if no .so name files, skip
	if len(newSonameFiles) == 0 {
		return nil
	}

	// fetch current latest apk
	p := existingPackages[newPackageName]

	if p == nil {
		o.Logger.Printf("no existing package found for %s, skipping so name check", newPackageName)
		return nil
	}
	existingFilename := fmt.Sprintf("%s-%s.apk", p.Name, p.Version)
	err = o.downloadCurrentAPK(existingFilename, dirExistingApk)
	if err != nil {
		return errors.Wrapf(err, "failed to download %s using base URL %s", newPackageName, o.ApkIndexURL)
	}

	// get any existing so names
	existingSonameFiles, err := o.getSonameFiles(dirExistingApk)
	if err != nil {
		return errors.Wrapf(err, "error when looking for soname files in existing apk")
	}

	err = o.checkSonamesMatch(existingSonameFiles, newSonameFiles)
	if err != nil {
		return errors.Wrapf(err, "soname files differ, this can cause an ABI break.  Existing soname files %s, New soname files %s", strings.Join(existingSonameFiles, ","), strings.Join(newSonameFiles, ","))
	}

	return nil
}

//nolint:gocritic // hugeParam for entry
func (o SoNameOptions) downloadCurrentAPK(newPackageName string, dirCurrentApk string) error {
	apkURL := strings.ReplaceAll(o.ApkIndexURL, "APKINDEX", newPackageName)
	resp, err := o.Client.Get(apkURL)
	if err != nil {
		return errors.Wrapf(err, "failed to get %s", apkURL)
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath.Join(dirCurrentApk, newPackageName))
	if err != nil {
		return err
	}
	defer out.Close()

	// Writer the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

//nolint:gocritic // hugeParam for entry
func (o SoNameOptions) getSonameFiles(dir string) ([]string, error) {
	reg := regexp.MustCompile(`.so.*\d`)

	var fileList []string
	err := filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		s := reg.FindString(filepath.Base(path))
		if s != "" {
			fileList = append(fileList, path)
		}
		return nil
	})

	return fileList, err
}

//nolint:gocritic // hugeParam for entry
func (o SoNameOptions) checkSonamesMatch(existingSonameFiles, newSonameFiles []string) error {
	reg := regexp.MustCompile(`.so.*\d`)

	// first turn the existing soname files into a map so it is easier to match with
	existingSonameMap := make(map[string]string)
	for _, filename := range existingSonameFiles {
		name := reg.Split(filename, -1)[0]
		version := reg.FindAllString(filename, -1)[0]

		existingSonameMap[name] = version
	}

	// now iterate over new soname files and compare with existing files
	for _, filename := range newSonameFiles {
		name := reg.Split(filename, -1)[0]
		version := reg.FindAllString(filename, -1)[0]

		existingVersion := existingSonameMap[name]
		// skip if no matching file
		if existingVersion == "" {
			continue
		}

		if existingVersion != version {
			return fmt.Errorf("soname version check failed, %s has an existing version %s while new package contains a different version %s.  This can cause ABI failures", name, existingVersion, version)
		}
	}
	return nil
}
