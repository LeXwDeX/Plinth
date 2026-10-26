#!/usr/bin/env python3
"""Read-only, full-enumeration audit of a private current-Go DB/CAS snapshot.

This does not run ETL, repair data, fetch URLs, or certify formal migration.
The capture's stopped-writer assertion is operator evidence, not authenticated here.
"""
import argparse
from collections import Counter, defaultdict
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import subprocess
import sys


class InvalidEvidence(Exception):
    pass


def digest(value):
    return hashlib.sha256(value.encode() if isinstance(value, str) else value).hexdigest()


def regular(path):
    path = Path(os.path.abspath(path))
    for ancestor in [path, *path.parents]:
        if ancestor.is_symlink():
            raise InvalidEvidence('symlink_input')
    if not path.is_file():
        raise InvalidEvidence('nonregular_input')
    return path


def private_file(path):
    path = regular(path)
    if path.stat().st_mode & 0o077 or path.parent.stat().st_mode & 0o077:
        raise InvalidEvidence('nonprivate_snapshot')
    return path


def hash_file(path):
    path = regular(path)
    before = path.stat()
    h = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            h.update(block)
    after = path.stat()
    if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns):
        raise InvalidEvidence('changed_input')
    return h.hexdigest()


def tree_manifest(root):
    root = Path(os.path.abspath(root))
    if root.is_symlink() or not root.is_dir():
        raise InvalidEvidence('invalid_storage_root')
    for ancestor in root.parents:
        if ancestor.is_symlink():
            raise InvalidEvidence('symlink_input')
    files = {}
    for parent, dirs, names in os.walk(root, followlinks=False):
        for name in dirs + names:
            path = Path(parent) / name
            mode = path.lstat().st_mode
            if stat.S_ISLNK(mode) or not (stat.S_ISREG(mode) or stat.S_ISDIR(mode)):
                raise InvalidEvidence('unsupported_storage_entry')
        for name in sorted(names):
            path = Path(parent) / name
            relative = path.relative_to(root).as_posix()
            if relative == '.maintenance.lock':
                continue  # Existing native checker owns this advisory lock file.
            files[relative] = {'size': path.stat().st_size, 'sha256': hash_file(path)}
    return files


def tree_digest(files):
    return digest(json.dumps(files, sort_keys=True, separators=(',', ':')))


def key_digest(key):
    match = re.fullmatch(r'blobs/([0-9a-f]{2})/([0-9a-f]{64})(?:\.[^/]+)?', key)
    return match[2] if match and match[1] == match[2][:2] else None


def media_type_allowed(allowed, media):
    if not allowed or media == '':
        return True
    return any(candidate == media or (candidate.endswith('/*') and media.startswith(candidate[:-1])) for candidate in allowed)


def requirement_for_asset(outputs, asset):
    # Match generation.requirementForClaim: exact > longest prefix > role wildcard.
    best, best_prefix_length = None, -1
    for requirement in outputs:
        if requirement['role'] != asset['role']:
            continue
        variant = requirement.get('variant', '')
        prefix = requirement.get('variant_prefix', '')
        if variant:
            if variant == asset['variant']:
                return requirement
        elif prefix:
            if asset['variant'].startswith(prefix) and len(prefix) > best_prefix_length:
                best, best_prefix_length = requirement, len(prefix)
        elif best_prefix_length < 0:
            best, best_prefix_length = requirement, 0
    return best


def analyze(db, files, native):
    """Return only fixed labels, counts and hashed identities; never raw payloads."""
    issues = []
    def issue(code, identity='', **counts):
        issues.append({'code': code, 'identity_sha256': digest(str(identity)), **counts})
    tables = {r[0] for r in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
    required = {'generation_tasks', 'generation_attempts', 'generation_assets', 'generation_input_assets', 'generation_artifact_claims', 'generation_recovery_receipts', 'generation_prepared_inputs', 'go_chat_sessions', 'etl_import_items', 'legacy_migration_records'}
    if not required <= tables:
        raise InvalidEvidence('unsupported_database_schema')
    if [r[0] for r in db.execute('PRAGMA integrity_check')] != ['ok']:
        issue('database_integrity')
    foreign = list(db.execute('PRAGMA foreign_key_check'))
    if foreign:
        issue('foreign_key_violation', count=len(foreign))
    counts = {table: db.execute('SELECT count(*) FROM ' + table).fetchone()[0] for table in sorted(required)}
    def rows(table):
        result = [dict(row) for row in db.execute('SELECT * FROM ' + table)]
        if len(result) != counts[table]:
            raise InvalidEvidence('row_count_mismatch')
        return result
    tasks = {r['id']: r for r in rows('generation_tasks')}
    attempts = {r['id']: r for r in rows('generation_attempts')}
    sessions = {r['id']: r['owner_id'] for r in rows('go_chat_sessions')}
    inputs = {r['id']: r for r in rows('generation_input_assets')}
    assets = rows('generation_assets')
    assets_by_id = {r['id']: r for r in assets}
    by_task = defaultdict(list)
    references = []
    valid_assets = set()
    def reference(kind, identity, asset):
        key = asset.get('storage_key', '')
        size = asset.get('size_bytes', asset.get('size'))
        sha = asset.get('sha256') or key_digest(key)
        references.append((kind, key))
        actual = files.get(key)
        if not key_digest(key) or sha != key_digest(key) or not isinstance(size, int) or isinstance(size, bool) or size < 0:
            issue('invalid_cas_reference', identity)
            return False
        if actual is None:
            issue('missing_cas_blob', identity)
            return False
        if actual != {'size': size, 'sha256': sha}:
            issue('cas_bytes_mismatch', identity)
            return False
        return True
    for asset in assets:
        task = tasks.get(asset['task_id'])
        attempt = attempts.get(asset['attempt_id'])
        if not task or not attempt or attempt['task_id'] != asset['task_id']:
            issue('asset_task_attempt_mismatch', asset['id'])
        by_task[asset['task_id']].append(asset)
        if reference('output', asset['id'], asset):
            valid_assets.add(asset['id'])
    for asset in inputs.values():
        if not asset['owner_id']:
            issue('empty_input_owner', asset['id'])
        reference('input', asset['id'], asset)
    for attempt in attempts.values():
        if attempt['task_id'] not in tasks:
            issue('orphan_attempt', attempt['id'])
    for claim in rows('generation_artifact_claims'):
        identity = claim['attempt_id'] + ':' + claim['claim_id']
        if claim['attempt_id'] not in attempts:
            issue('orphan_claim', identity)
        if claim['stored_asset_json'] is not None:
            reference('claim', identity, json.loads(claim['stored_asset_json']))
    for row in rows('generation_recovery_receipts'):
        receipt = json.loads(row['payload_json'])
        source = receipt['source']
        task = tasks.get(row['source_task_id'])
        attempt = attempts.get(row['source_attempt_id'])
        if (receipt.get('id') != row['id'] or receipt.get('digest') != row['digest'] or not task or not attempt or
            source.get('owner_id') != row['owner_id'] or source.get('task_id') != row['source_task_id'] or
            source.get('attempt_id') != row['source_attempt_id'] or source.get('session_id') != row['session_id'] or
            task['owner_id'] != row['owner_id'] or attempt['task_id'] != task['id'] or
            sessions.get(row['session_id']) != row['owner_id']):
            issue('recovery_owner_source_mismatch', row['id'])
        for index, slot in enumerate(receipt['slots']):
            reference('recovery', row['id'] + ':' + str(index), slot)
    observations = Counter()
    for prepared in rows('generation_prepared_inputs'):
        payload = json.loads(prepared['payload_json'])
        owner = prepared['owner_id']
        if (payload.get('owner_id') != owner or payload.get('session_id', '') != prepared['session_id'] or
            (prepared['session_id'] in sessions and sessions[prepared['session_id']] != owner)):
            issue('prepared_owner_mismatch', prepared['id'])
        if prepared['session_id'] and prepared['session_id'] not in sessions:
            observations['prepared_session_absent'] += 1
        for frozen in payload.get('assets', []):
            asset = inputs.get(frozen['id']) or assets_by_id.get(frozen['id'])
            actual_owner = asset.get('owner_id') if asset else None
            if asset and 'task_id' in asset:
                actual_owner = tasks.get(asset['task_id'], {}).get('owner_id')
            if not asset or actual_owner != owner:
                issue('prepared_asset_owner_mismatch', prepared['id'])
            elif (frozen['sha256'], frozen['size'], frozen['media_type']) != (asset['sha256'], asset['size_bytes'], asset['media_type']):
                issue('prepared_asset_bytes_mismatch', prepared['id'])
        for dependency in payload.get('dependencies', []):
            task = tasks.get(dependency['task_id'])
            if not task or task['owner_id'] != owner:
                issue('prepared_dependency_owner_mismatch', prepared['id'])
            for frozen in dependency.get('output_assets', []):
                asset = assets_by_id.get(frozen['id'])
                if not asset or asset['task_id'] != dependency['task_id']:
                    issue('prepared_dependency_asset_mismatch', prepared['id'])
                elif (frozen['sha256'], frozen['size'], frozen['media_type']) != (asset['sha256'], asset['size_bytes'], asset['media_type']):
                    issue('prepared_dependency_bytes_mismatch', prepared['id'])
    state_counts, resources = Counter(), Counter()
    required_slots = 0
    offerings = []
    for task in tasks.values():
        state = task['state']
        if state not in {'SUCCEEDED', 'FAILED', 'CANCELED', 'QUEUED', 'RUNNING', 'MATERIALIZING'}:
            issue('unknown_task_state', task['id'])
        else:
            state_counts[state] += 1
        if not task['owner_id'] or (task['session_id'] and sessions.get(task['session_id']) != task['owner_id']):
            issue('task_session_owner_mismatch', task['id'])
        if task['current_attempt_id'] and attempts.get(task['current_attempt_id'], {}).get('task_id') != task['id']:
            issue('current_attempt_mismatch', task['id'])
        offering = json.loads(task['offering_json'])
        outputs = offering.get('outputs')
        offerings.append((digest(task['id']), digest(task['offering_json'])))
        if not isinstance(outputs, list):
            issue('missing_frozen_output_contract', task['id'])
            continue
        # Minimum assignment below only marks the required subset. Every persisted
        # output still obeys its resolved frozen contract, including minimum=0,
        # background/on-demand, and additional outputs beyond the minimum.
        for asset in by_task[task['id']]:
            if asset['size_bytes'] <= 0:
                issue('output_empty_asset', asset['id'])
            requirement = requirement_for_asset(outputs, asset)
            if requirement is None:
                continue  # Runtime does not constrain an undeclared claim.
            if not media_type_allowed(requirement.get('media_types', []), asset['media_type']):
                issue('output_media_type_mismatch', asset['id'])
            if requirement.get('max_bytes', 0) > 0 and asset['size_bytes'] > requirement['max_bytes']:
                issue('output_size_limit', asset['id'])
            if asset.get('delivery_profile', '') != requirement.get('delivery_profile', ''):
                issue('output_delivery_profile_mismatch', asset['id'])
        if state != 'SUCCEEDED':
            continue  # Failed/canceled history does not promise minimum output coverage.
        if not outputs:
            issue('empty_success_output_contract', task['id'])
        available = sorted((a for a in by_task[task['id']] if a['id'] in valid_assets), key=lambda a: (a['ordinal'], a['id']))
        assigned = set()
        for index, requirement in enumerate(outputs):
            delivery = requirement.get('delivery', '')
            if delivery not in {'', 'required', 'background', 'on-demand'}:
                issue('unknown_delivery_contract', task['id'])
                continue
            if delivery in {'background', 'on-demand'}:
                continue
            required_slots += 1
            minimum = requirement.get('minimum')
            minimum = 0 if delivery == 'required' and minimum == 0 else (minimum if isinstance(minimum, int) and minimum > 0 else 1)
            def matches(asset):
                variant = asset.get('variant', '')
                return (asset['role'] == requirement['role'] and
                        (not requirement.get('variant') or variant == requirement['variant']) and
                        (requirement.get('variant') or not requirement.get('variant_prefix') or variant.startswith(requirement['variant_prefix'])))
            matching = [a for a in available if a['id'] not in assigned and matches(a)]
            if len(matching) < minimum:
                issue('success_missing_required_output', task['id'] + ':' + str(index), required=minimum, found=len(matching))
            for asset in matching[:minimum]:
                assigned.add(asset['id'])
                if not asset['required']:
                    issue('required_output_flag_mismatch', asset['id'])
        for key, resource in json.loads(task['resource_state_json'] or '{}').items():
            value = resource.get('state')
            if value not in {'ready', 'pending', 'incomplete', 'failed', 'denied', 'abandoned'}:
                issue('unknown_resource_state', task['id'])
                continue
            resources[value] += 1
            parts = key.split('|')
            if len(parts) != 3:
                issue('invalid_resource_key', task['id'])
                continue
            role, scope, group = parts
            declarations = [r for r in outputs if r['role'] == role and r.get('group', '') == group and (r.get('variant') == scope or (r.get('variant_prefix') and scope.startswith(r['variant_prefix'])) or (not r.get('variant') and not r.get('variant_prefix')))]
            if not declarations:
                issue('undeclared_resource', task['id'])
            elif value != 'ready' and any(r.get('delivery', '') not in {'background', 'on-demand'} for r in declarations):
                issue('success_required_resource_not_ready', task['id'])
            elif value == 'ready' and not any(a['role'] == role and (not scope or a['variant'] == scope or any(r.get('variant_prefix') == scope and a['variant'].startswith(scope) for r in declarations)) for a in available):
                # Explicit required minimum=0 is permitted to have no returned claim.
                if not all(r.get('delivery') == 'required' and r.get('minimum') == 0 for r in declarations):
                    issue('ready_without_asset', task['id'])
    referenced = {key for _, key in references}
    for key, value in files.items():
        sha = key_digest(key)
        if not sha:
            issue('unknown_storage_file', key)
        elif sha != value['sha256']:
            issue('cas_filename_digest_mismatch', key)
    if native.get('read_only') is not True or native.get('issue_count') != 0:
        issue('native_storage_check_failed')
    if native.get('reference_count') != len(references) or native.get('blob_count') != len(referenced):
        issue('native_reference_count_mismatch')
    legacy_absent = not {'asset_registry', 'task_cache', 'task_history'} & tables and counts['etl_import_items'] == counts['legacy_migration_records'] == 0
    return {'table_counts': counts, 'observations': dict(observations), 'task_states': dict(state_counts), 'resource_states': dict(resources),
            'successful_required_slots': required_slots, 'frozen_offerings_sha256': digest(json.dumps(sorted(offerings))),
            'references': dict(Counter(kind for kind, _ in references)), 'referenced_blobs': len(referenced),
            'unreferenced_blobs': len(set(files) - referenced),
            'legacy_hypotheses': {name: ('not_applicable_to_current_go_records' if legacy_absent else 'unknown') for name in ['H2', 'H5', 'H6']},
            'issues': issues, 'issue_count': len(issues)}


def run(args):
    database, identity_path, checker = map(regular, [args.database, args.identity, args.storage_checker])
    private_file(database)
    private_file(identity_path)
    storage = Path(os.path.abspath(args.storage))
    report_path = Path(os.path.abspath(args.report))
    for source in [database, identity_path, checker]:
        if report_path == source:
            raise InvalidEvidence('unsafe_report_location')
    if report_path == storage or storage in report_path.parents or report_path.exists() or report_path.is_symlink():
        raise InvalidEvidence('unsafe_report_location')
    for ancestor in [report_path.parent, *report_path.parents]:
        if ancestor.is_symlink():
            raise InvalidEvidence('unsafe_report_location')
    if any(Path(str(database) + suffix).exists() for suffix in ['-wal', '-shm', '-journal']):
        raise InvalidEvidence('snapshot_has_journal')
    identity_bytes = identity_path.read_bytes()
    identity = json.loads(identity_bytes)
    if (identity.get('target') != 'development' or identity.get('consistency') != 'stopped_art_ai_writers' or
        identity.get('snapshot_complete') is not True or not re.fullmatch('[0-9a-f]{40}', identity.get('revision', '')) or
        not re.fullmatch('sha256:[0-9a-f]{64}', identity.get('image_id', '')) or
        not re.fullmatch('[0-9a-f]{64}', identity.get('container_id', ''))):
        raise InvalidEvidence('invalid_capture_identity')
    database_sha, checker_sha = hash_file(database), hash_file(checker)
    files = tree_manifest(storage)
    # Run the existing Go receipt validator and complete CAS reference collector.
    native = subprocess.run([str(checker), '--check', '--database', str(database), '--storage', str(storage)], capture_output=True)
    if native.returncode:
        raise InvalidEvidence('native_storage_checker_error')
    native_report = json.loads(native.stdout)
    db = sqlite3.connect(database.as_uri() + '?mode=ro&immutable=1', uri=True)
    db.row_factory = sqlite3.Row
    try:
        db.execute('PRAGMA query_only=ON')
        db.execute('BEGIN')
        result = analyze(db, files, native_report)
    finally:
        db.close()
    if hash_file(database) != database_sha or identity_path.read_bytes() != identity_bytes or hash_file(checker) != checker_sha or tree_manifest(storage) != files:
        raise InvalidEvidence('changed_input')
    if any(Path(str(database) + suffix).exists() for suffix in ['-wal', '-shm', '-journal']):
        raise InvalidEvidence('snapshot_has_journal')
    result.update({'version': 1, 'target': 'development', 'read_only': True, 'sampling': 'full',
                   'capture_consistency': 'operator_attested_stopped_art_ai_writers',
                   'container_id': identity['container_id'], 'image_id': identity['image_id'], 'revision': identity['revision'],
                   'database_sha256': database_sha, 'identity_sha256': digest(identity_bytes), 'checker_sha256': checker_sha,
                   'generator_sha256': hash_file(__file__), 'native_report_sha256': digest(native.stdout),
                   'storage_manifest_sha256': tree_digest(files), 'storage_files': len(files), 'storage_bytes': sum(f['size'] for f in files.values()),
                   'source_loss_approval': False, 'status': 'pass' if not result['issues'] else 'stop',
                   'limits': ['capture_identity_is_operator_supplied', 'no_remote_fetch_or_formal_migration_certification', 'no_model_container_semantic_or_authenticated_http_check', 'legacy_mounts_not_copied_when_no_current_legacy_records']})
    with os.fdopen(os.open(report_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as out:
        json.dump(result, out, sort_keys=True, indent=2)
        out.write('\n')
    return 0 if result['status'] == 'pass' else 2


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['database', 'storage', 'identity', 'storage-checker', 'report']:
        parser.add_argument('--' + name, required=True)
    try:
        return run(parser.parse_args())
    except (InvalidEvidence, OSError, sqlite3.Error, ValueError, KeyError, TypeError):
        # Underlying exceptions can contain database text, paths, URLs or owner IDs.
        print('STOP: invalid, changed or unsupported private evidence; no report accepted', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
