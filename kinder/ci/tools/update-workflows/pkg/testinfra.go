/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pkg

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	versionutil "k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/yaml"
)

func processTestInfra(settings *Settings, cfg *jobGroup, oldestVer, minVer *versionutil.Version) error {
	log.Infof("processing test-infra jobs for jobGroup %q", cfg.Name)

	if len(cfg.TestInfraJobSpec.Template) == 0 {
		log.Infof("empty TestInfra.Template; skipping test-infra jobs for jobGroup %q", cfg.Name)
		return nil
	}

	// prepare job template
	var templateJob *template.Template
	tPath := cfg.TestInfraJobSpec.Template
	if !path.IsAbs(tPath) {
		tPath = filepath.Join(filepath.Dir(settings.PathConfig), tPath)
	}
	tBytes, err := os.ReadFile(tPath)
	if err != nil {
		return err
	}
	templateJob, err = template.New("job-template").Funcs(template.FuncMap{
		"dashVer":       dashVer,
		"ciLabelFor":    ciLabelFor,
		"imageVer":      imageVer,
		"branchFor":     branchFor,
		"sigReleaseVer": sigReleaseVer,
	}).Parse(string(tBytes))
	if err != nil {
		return err
	}

	// prepare output file name template
	templateFileName, err := template.New("file-name").Parse(cfg.KinderWorkflowSpec.TargetFile)
	if err != nil {
		return err
	}

	str := autogeneratedHeader + "\nperiodics:\n"

	for i, job := range cfg.Jobs {
		log.Infof("processing Job index %d, %#v", i, job)

		if skipVersion(oldestVer, minVer, job.KubernetesVersion) {
			log.Infof("skipping Job index %d, %#v", i, job)
			continue
		}

		// prepare variables for template replacement
		vars := templateVars{
			KubernetesVersion: job.KubernetesVersion,
			KubeadmVersion:    job.KubeadmVersion,
			KubeletVersion:    job.KubeletVersion,
			InitVersion:       job.InitVersion,
			UpgradeVersion:    job.UpgradeVersion,
			TargetFile:        cfg.TestInfraJobSpec.TargetFile,

			TestInfraImage: settings.ImageTestInfra,
		}

		// update file to run in the test-infra job
		buf := bytes.Buffer{}
		if err := templateFileName.Execute(&buf, vars); err != nil {
			return err
		}
		vars.WorkflowFile = "\"" + strings.TrimSuffix(buf.String(), ".yaml") + "\""

		// set job period and alerts
		var failures, staleResults int
		if job.KubernetesVersion == latestVersion || job.KubeadmVersion == latestVersion {
			vars.JobInterval = "2h"
			failures = 8
			staleResults = 16
		} else {
			vars.JobInterval = "12h"
			failures = 4
			staleResults = 48

		}
		vars.AlertAnnotations = fmt.Sprintf("    testgrid-num-failures-to-alert: \"%d\"\n"+
			"    testgrid-alert-stale-results-hours: \"%d\"", failures, staleResults)

		// execute main template
		buf.Reset()
		if err := templateJob.Execute(&buf, vars); err != nil {
			return err
		}
		str += "\n" + buf.String()
	}

	// unmarshal the YAML to validate it
	if err = yaml.Unmarshal([]byte(str), struct{}{}); err != nil {
		return errors.Wrapf(err, "\n%s\n", str)
	}

	// write testinfra job file
	outPath := filepath.Join(settings.PathTestInfra, path.Base(cfg.TestInfraJobSpec.TargetFile))
	log.Infof("writing %q", outPath)
	if err := os.WriteFile(outPath, []byte(str), 0644); err != nil {
		return err
	}
	return nil
}
