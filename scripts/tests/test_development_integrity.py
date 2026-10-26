import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('development_integrity', ROOT / 'scripts/check-development-integrity.py')
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class IntegrityTest(unittest.TestCase):
    def setUp(self):
        self.db = sqlite3.connect(':memory:')
        self.db.row_factory = sqlite3.Row
        self.db.executescript('''
        CREATE TABLE generation_tasks(id TEXT, owner_id TEXT, state TEXT, session_id TEXT, current_attempt_id TEXT, offering_json TEXT, resource_state_json TEXT);
        CREATE TABLE generation_attempts(id TEXT, task_id TEXT);
        CREATE TABLE generation_assets(id TEXT, task_id TEXT, attempt_id TEXT, role TEXT, variant TEXT, ordinal INTEGER, required INTEGER, storage_key TEXT, media_type TEXT, size_bytes INTEGER, sha256 TEXT, delivery_profile TEXT);
        CREATE TABLE generation_input_assets(id TEXT, owner_id TEXT, storage_key TEXT, media_type TEXT, size_bytes INTEGER, sha256 TEXT);
        CREATE TABLE generation_artifact_claims(attempt_id TEXT, claim_id TEXT, stored_asset_json TEXT);
        CREATE TABLE generation_recovery_receipts(id TEXT, digest TEXT, payload_json TEXT, owner_id TEXT, source_task_id TEXT, source_attempt_id TEXT, session_id TEXT);
        CREATE TABLE generation_prepared_inputs(id TEXT, owner_id TEXT, session_id TEXT, payload_json TEXT);
        CREATE TABLE go_chat_sessions(id TEXT, owner_id TEXT);
        CREATE TABLE etl_import_items(id TEXT);
        CREATE TABLE legacy_migration_records(id TEXT);
        ''')
        self.sha = audit.digest(b'model')
        self.key = 'blobs/' + self.sha[:2] + '/' + self.sha + '.glb'
        self.files = {self.key: {'size': 5, 'sha256': self.sha}}
        self.native = {'read_only': True, 'issue_count': 0, 'reference_count': 1, 'blob_count': 1}
        self.outputs = [{'role': 'model', 'variant': 'glb', 'delivery': 'required', 'minimum': 1}]
        self.db.execute('INSERT INTO go_chat_sessions VALUES (?,?)', ('session-secret', 'owner-secret'))
        self.db.execute('INSERT INTO generation_tasks VALUES (?,?,?,?,?,?,?)', ('task-secret', 'owner-secret', 'SUCCEEDED', 'session-secret', 'attempt-secret', json.dumps({'outputs': self.outputs}), '{}'))
        self.db.execute('INSERT INTO generation_attempts VALUES (?,?)', ('attempt-secret', 'task-secret'))
        self.db.execute('INSERT INTO generation_assets VALUES (?,?,?,?,?,?,?,?,?,?,?,?)', ('asset-secret', 'task-secret', 'attempt-secret', 'model', 'glb', 0, 1, self.key, 'model/gltf-binary', 5, self.sha, ''))

    def tearDown(self):
        self.db.close()

    def result(self):
        return audit.analyze(self.db, self.files, self.native)

    def codes(self):
        return [i['code'] for i in self.result()['issues']]

    def set_outputs(self, outputs):
        self.db.execute('UPDATE generation_tasks SET offering_json=?', (json.dumps({'outputs': outputs}),))

    def test_valid_snapshot_and_redaction(self):
        report = self.result()
        self.assertEqual(report['issue_count'], 0)
        self.assertEqual(report['legacy_hypotheses']['H2'], 'not_applicable_to_current_go_records')
        for secret in ['task-secret', 'owner-secret', 'asset-secret', self.key]:
            self.assertNotIn(secret, json.dumps(report))

    def test_missing_and_corrupt_bytes_cannot_satisfy_success(self):
        self.files.clear()
        self.assertIn('missing_cas_blob', self.codes())
        self.assertIn('success_missing_required_output', self.codes())
        self.files[self.key] = {'size': 5, 'sha256': '0' * 64}
        self.assertIn('cas_bytes_mismatch', self.codes())
        self.assertIn('success_missing_required_output', self.codes())

    def test_frozen_minimum_variants_and_single_assignment(self):
        self.set_outputs(self.outputs * 2)
        self.assertIn('success_missing_required_output', self.codes())
        self.set_outputs([{'role': 'model', 'variant_prefix': 'fbx', 'minimum': 1}])
        self.assertIn('success_missing_required_output', self.codes())
        self.set_outputs([{'role': 'model', 'variant_prefix': 'g', 'minimum': 1}])
        self.assertEqual(self.codes(), [])

    def test_background_and_conditional_absence_and_failed_history(self):
        self.db.execute('DELETE FROM generation_assets')
        self.native.update(reference_count=0, blob_count=0)
        self.set_outputs([{'role': 'model', 'delivery': 'background'}, {'role': 'report', 'delivery': 'required', 'minimum': 0}])
        self.assertEqual(self.codes(), [])
        self.set_outputs(self.outputs)
        self.db.execute("UPDATE generation_tasks SET state='FAILED'")
        self.assertEqual(self.codes(), [])

    def test_schema_media_wildcard_matches_runtime(self):
        self.set_outputs([dict(self.outputs[0], media_types=['model/*'])])
        self.assertEqual(self.codes(), [])
        self.set_outputs([dict(self.outputs[0], media_types=['image/*'])])
        self.assertIn('output_media_type_mismatch', self.codes())

    def test_conditional_returned_output_obeys_full_contract(self):
        self.set_outputs([dict(self.outputs[0], minimum=0, media_types=['image/png'], max_bytes=1, delivery_profile='expected-profile')])
        self.db.execute('UPDATE generation_assets SET required=0')
        codes = self.codes()
        self.assertIn('output_media_type_mismatch', codes)
        self.assertIn('output_size_limit', codes)
        self.assertIn('output_delivery_profile_mismatch', codes)
        self.assertNotIn('required_output_flag_mismatch', codes)
        self.db.execute('DELETE FROM generation_assets')
        self.native.update(reference_count=0, blob_count=0)
        self.assertEqual(self.codes(), [])

    def test_extra_output_is_checked_without_requiring_extra_flag(self):
        self.set_outputs([dict(self.outputs[0], media_types=['model/gltf-binary'], max_bytes=10)])
        self.db.execute('INSERT INTO generation_assets SELECT ?,task_id,attempt_id,role,variant,1,0,storage_key,?,size_bytes,sha256,? FROM generation_assets', ('extra', 'image/png', 'unexpected-profile'))
        self.native['reference_count'] = 2
        self.assertIn('output_media_type_mismatch', self.codes())
        self.assertIn('output_delivery_profile_mismatch', self.codes())
        self.assertNotIn('required_output_flag_mismatch', self.codes())
        self.db.execute("UPDATE generation_assets SET media_type='model/gltf-binary',delivery_profile='' WHERE id='extra'")
        self.assertEqual(self.codes(), [])

    def test_output_contract_uses_exact_then_longest_prefix(self):
        broad = {'role': 'model', 'delivery': 'background', 'media_types': ['image/png'], 'max_bytes': 1, 'delivery_profile': 'wrong'}
        exact = dict(self.outputs[0], media_types=['model/gltf-binary'], max_bytes=10)
        prefix = dict(broad, variant_prefix='g')
        self.set_outputs([broad, prefix, exact])
        self.assertEqual(self.codes(), [])
        longest = dict(prefix, variant_prefix='gl', media_types=['model/gltf-binary'], max_bytes=10, delivery_profile='')
        self.set_outputs([broad, prefix, longest])
        self.assertEqual(self.codes(), [])
        self.set_outputs([longest, broad, prefix])
        self.assertEqual(self.codes(), [])
        self.set_outputs([longest, dict(exact, media_types=['image/png'])])
        self.assertIn('output_media_type_mismatch', self.codes())

    def test_conditional_empty_returned_output_is_rejected(self):
        self.set_outputs([dict(self.outputs[0], minimum=0)])
        sha = audit.digest(b'')
        key = 'blobs/' + sha[:2] + '/' + sha + '.glb'
        self.db.execute('UPDATE generation_assets SET required=0,size_bytes=0,storage_key=?,sha256=?', (key, sha))
        self.files = {key: {'size': 0, 'sha256': sha}}
        self.assertIn('output_empty_asset', self.codes())
        self.assertNotIn('required_output_flag_mismatch', self.codes())

    def test_owner_and_attempt_isolation(self):
        self.db.execute("UPDATE go_chat_sessions SET owner_id='other-owner'")
        self.db.execute("UPDATE generation_attempts SET task_id='other-task'")
        self.assertIn('task_session_owner_mismatch', self.codes())
        self.assertIn('asset_task_attempt_mismatch', self.codes())

    def test_prepared_asset_cross_owner(self):
        payload = {'owner_id': 'other-owner', 'session_id': '', 'assets': [{'id': 'asset-secret'}]}
        self.db.execute('INSERT INTO generation_prepared_inputs VALUES (?,?,?,?)', ('prep-secret', 'other-owner', '', json.dumps(payload)))
        self.assertIn('prepared_asset_owner_mismatch', self.codes())

    def test_ready_requires_asset_and_native_findings_fail(self):
        self.db.execute('UPDATE generation_tasks SET resource_state_json=?', (json.dumps({'image|png|': {'state': 'ready'}}),))
        self.set_outputs(self.outputs + [{'role': 'image', 'variant': 'png', 'delivery': 'background'}])
        self.assertIn('ready_without_asset', self.codes())
        self.native['issue_count'] = 1
        self.assertIn('native_storage_check_failed', self.codes())

    def test_retained_prepared_session_absence_is_not_cross_owner(self):
        payload = {'owner_id': 'owner-secret', 'session_id': 'deleted-session'}
        self.db.execute('INSERT INTO generation_prepared_inputs VALUES (?,?,?,?)', ('prep-secret', 'owner-secret', 'deleted-session', json.dumps(payload)))
        self.assertEqual(self.codes(), [])
        self.assertEqual(self.result()['observations']['prepared_session_absent'], 1)
        self.db.execute('INSERT INTO go_chat_sessions VALUES (?,?)', ('deleted-session', 'other-owner'))
        self.assertIn('prepared_owner_mismatch', self.codes())

    def test_required_resource_terminal_conflict_and_legacy_absence(self):
        for state in ['pending', 'incomplete', 'failed', 'denied', 'abandoned']:
            self.db.execute('UPDATE generation_tasks SET resource_state_json=?', (json.dumps({'model|glb|': {'state': state}}),))
            self.assertIn('success_required_resource_not_ready', self.codes())
        for state in ['ready']:
            self.db.execute('UPDATE generation_tasks SET resource_state_json=?', (json.dumps({'model|glb|': {'state': state}}),))
            self.assertEqual(self.codes(), [])
        self.db.execute("UPDATE generation_tasks SET resource_state_json='{}'")
        self.assertEqual(self.codes(), [])

    def test_attempt_states_are_not_public_task_states(self):
        for state in ['SUBMITTING', 'RETRY_WAIT', 'UNRECOGNIZED']:
            self.db.execute('UPDATE generation_tasks SET state=?', (state,))
            self.assertIn('unknown_task_state', self.codes())
        for state in ['QUEUED', 'RUNNING', 'MATERIALIZING', 'SUCCEEDED', 'FAILED', 'CANCELED']:
            self.db.execute('UPDATE generation_tasks SET state=?', (state,))
            self.assertNotIn('unknown_task_state', self.codes())

    def test_private_inputs_required(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve() / 'db'
            path.write_bytes(b'private')
            path.chmod(0o600)
            self.assertEqual(audit.private_file(path), path)
            path.chmod(0o644)
            with self.assertRaises(audit.InvalidEvidence):
                audit.private_file(path)
            path.chmod(0o600)
            path.parent.chmod(0o755)
            with self.assertRaises(audit.InvalidEvidence):
                audit.private_file(path)
            path.parent.chmod(0o700)

    def test_legacy_records_do_not_get_not_applicable(self):
        self.db.execute("INSERT INTO legacy_migration_records VALUES ('old')")
        self.assertEqual(self.result()['legacy_hypotheses']['H5'], 'unknown')

    def test_paths_symlinks_and_full_enumeration(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            (root / 'blobs').mkdir()
            (root / 'blobs/one').write_bytes(b'a')
            (root / 'blobs/two').write_bytes(b'b')
            self.assertEqual(len(audit.tree_manifest(root)), 2)
            (root / 'blobs/link').symlink_to('/etc/passwd')
            with self.assertRaises(audit.InvalidEvidence):
                audit.tree_manifest(root)
        self.assertIsNone(audit.key_digest('../escape'))
        self.assertIsNone(audit.key_digest('blobs/00/' + self.sha))

    def test_file_mutation_is_rejected(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp).resolve() / 'file'
            path.write_bytes(b'original')
            original = Path.open
            def changed_open(current, *args, **kwargs):
                stream = original(current, *args, **kwargs)
                if current == path:
                    with original(path, 'wb') as out:
                        out.write(b'changed-size')
                return stream
            with patch.object(Path, 'open', changed_open), self.assertRaises(audit.InvalidEvidence):
                audit.hash_file(path)


if __name__ == '__main__':
    unittest.main()
