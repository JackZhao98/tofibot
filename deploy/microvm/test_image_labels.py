import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def final_stage_labels(path):
    text = (ROOT / path).read_text().replace("\\\n", " ")
    stage = text[text.rindex("\nFROM "):]
    labels = {}
    for line in stage.splitlines():
        if line.startswith("LABEL "):
            for key, value in re.findall(r'([\w.-]+)=("[^"]*"|\S+)', line[6:]):
                labels[key] = value.strip('"')
    return stage, labels


class ImageLabelTests(unittest.TestCase):
    def test_app_image_declares_runtime_schema_and_revision(self):
        stage, labels = final_stage_labels("Dockerfile")
        self.assertEqual(labels["io.tofi.account-runtime"], "1")
        self.assertEqual(labels["io.tofi.data-schema"], "tofi-account-data-v1")
        self.assertEqual(labels["io.tofi.account-guest-protocol"], "tofi-account-guest-v1")
        self.assertEqual(labels["org.opencontainers.image.revision"], "$TOFI_SOURCE_COMMIT")
        self.assertIn("ARG TOFI_SOURCE_COMMIT", stage)

    def test_worker_image_declares_guest_protocol_and_revision(self):
        stage, labels = final_stage_labels("deploy/microvm/Worker.Dockerfile")
        self.assertEqual(labels["io.tofi.account-worker"], "1")
        self.assertEqual(labels["io.tofi.account-guest-protocol"], "tofi-account-guest-v1")
        self.assertEqual(labels["org.opencontainers.image.revision"], "$TOFI_SOURCE_COMMIT")
        self.assertIn("ARG TOFI_SOURCE_COMMIT", stage)

    def test_app_and_worker_agree_on_guest_protocol(self):
        _, app = final_stage_labels("Dockerfile")
        _, worker = final_stage_labels("deploy/microvm/Worker.Dockerfile")
        self.assertEqual(app["io.tofi.account-guest-protocol"], worker["io.tofi.account-guest-protocol"])


if __name__ == "__main__":
    unittest.main()
