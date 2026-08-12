import { spawnSync } from 'node:child_process';
import {
  chmodSync,
  copyFileSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const nativeEdgeRoot = path.join(repoRoot, 'deploy/native-edge');
const scriptsRoot = path.join(nativeEdgeRoot, 'scripts');
const systemdRoot = path.join(nativeEdgeRoot, 'systemd');
const tempDirs = [];

const tempDir = (prefix) => {
  const directory = mkdtempSync(path.join(os.tmpdir(), prefix));
  tempDirs.push(directory);
  return directory;
};

const run = (command, args, options = {}) =>
  spawnSync(command, args, {
    cwd: repoRoot,
    encoding: 'utf8',
    ...options,
  });

const readTree = (root) => {
  const files = [];
  const visit = (directory) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const absolutePath = path.join(directory, entry.name);
      if (entry.isDirectory()) visit(absolutePath);
      else if (entry.isFile()) files.push(absolutePath);
    }
  };
  visit(root);
  return files;
};

afterEach(() => {
  for (const directory of tempDirs.splice(0)) {
    rmSync(directory, { recursive: true, force: true });
  }
});

describe('native edge deployment', () => {
  it('keeps shell, Python, JSON, and systemd templates structurally valid', () => {
    const shellScripts = [
      'cpa-web-ingress-allow',
      'cpamp-postgres-backup',
      'verify-native-edge',
    ];
    for (const script of shellScripts) {
      const result = run('bash', ['-n', path.join(scriptsRoot, script)]);
      expect(result.status, result.stderr).toBe(0);
    }

    const pythonScript = path.join(scriptsRoot, 'cpa-auth-proxy-audit');
    const python = run('python3', [
      '-c',
      'import pathlib,sys; compile(pathlib.Path(sys.argv[1]).read_text(), sys.argv[1], "exec")',
      pythonScript,
    ]);
    expect(python.status, python.stderr).toBe(0);

    for (const file of ['config.json.example', 'runtime-config.json.example']) {
      expect(() => JSON.parse(readFileSync(path.join(nativeEdgeRoot, 'config', file), 'utf8'))).not.toThrow();
    }

    const expectedUnits = [
      'cliproxyapi.service',
      'cpa-manager-plus-separated-api.service',
      'cpa-web-ingress-allow.service',
      'cpa-web-ingress-allow.timer',
      'cpamp-postgres-backup.service',
      'cpamp-postgres-backup.timer',
      'cpamp-usage-persist.service',
      'cpamp-usage-redis.service',
      'cpamp-usage-rollup-daily.service',
      'cpamp-usage-rollup-hourly.service',
      'mihomo.service',
    ];
    expect(readdirSync(systemdRoot).sort()).toEqual(expectedUnits.sort());
  });

  it('keeps production secrets and host-specific values out of the package', () => {
    const content = readTree(nativeEdgeRoot)
      .filter((file) => !file.includes(`${path.sep}__pycache__${path.sep}`))
      .map((file) => readFileSync(file, 'utf8'))
      .join('\n');

    expect(content).not.toMatch(/\beyJ[A-Za-z0-9_-]{40,}\b/);
    expect(content).not.toMatch(/\brt\.[A-Za-z0-9._-]{30,}\b/);
    expect(content).not.toMatch(/[A-Za-z0-9._%+-]+@(gmail|outlook|icloud)\.com/i);
    expect(content).not.toMatch(/postgres(?:ql)?:\/\/[^\s<]+:[^\s<]+@/i);
  });

  it('routes ordinary and future CPA paths without forcing every connection to upgrade', () => {
    const site = readFileSync(
      path.join(nativeEdgeRoot, 'nginx/cpa-native-edge.conf.example'),
      'utf8'
    );
    const common = readFileSync(
      path.join(nativeEdgeRoot, 'nginx/cpa-proxy-common.conf'),
      'utf8'
    );

    expect(site).toContain('map $http_upgrade $connection_upgrade');
    expect(site).toMatch(/location \/ \{[\s\S]*proxy_pass http:\/\/cpa_native_api;/);
    expect(site).toContain('proxy_pass http://cpamp_separated_api/;');
    expect(common).toContain('proxy_set_header Connection $connection_upgrade;');
    expect(common).not.toContain('Connection "upgrade"');
  });

  it('renders as a valid Nginx configuration with TLS enabled', () => {
    if (process.platform === 'win32') return;
    if (!existsSync('/usr/sbin/nginx') || !existsSync('/usr/bin/openssl')) return;

    const prefix = tempDir('cpamp-nginx-');
    for (const directory of [
      'conf.d',
      'logs',
      'snippets',
      'ssl/public.example.com',
    ]) {
      mkdirSync(path.join(prefix, directory), { recursive: true });
    }
    copyFileSync(
      path.join(nativeEdgeRoot, 'nginx/cpa-native-edge.conf.example'),
      path.join(prefix, 'conf.d/cpa-native-edge.conf')
    );
    copyFileSync(
      path.join(nativeEdgeRoot, 'nginx/cpa-proxy-common.conf'),
      path.join(prefix, 'snippets/cpa-proxy-common.conf')
    );
    writeFileSync(
      path.join(prefix, 'nginx.conf'),
      [
        'error_log logs/error.log;',
        'pid logs/nginx.pid;',
        'events { worker_connections 64; }',
        'http {',
        '  access_log off;',
        '  include conf.d/*.conf;',
        '}',
        '',
      ].join('\n')
    );

    const certificate = path.join(prefix, 'ssl/public.example.com/fullchain.crt');
    const privateKey = path.join(prefix, 'ssl/public.example.com/private.key');
    const openssl = run(
      '/usr/bin/openssl',
      [
        'req',
        '-x509',
        '-newkey',
        'rsa:2048',
        '-nodes',
        '-keyout',
        privateKey,
        '-out',
        certificate,
        '-days',
        '1',
        '-subj',
        '/CN=public.example.com',
      ],
      { stdio: 'ignore' }
    );
    expect(openssl.status).toBe(0);

    const nginx = run('/usr/sbin/nginx', [
      '-t',
      '-p',
      `${prefix}/`,
      '-c',
      'nginx.conf',
    ]);
    expect(nginx.status, `${nginx.stdout}\n${nginx.stderr}`).toBe(0);
  });

  it('keeps CPA dependent on Mihomo and keeps DSNs out of worker command lines', () => {
    const cpaUnit = readFileSync(path.join(systemdRoot, 'cliproxyapi.service'), 'utf8');
    expect(cpaUnit).toContain('After=network-online.target mihomo.service');
    expect(cpaUnit).toContain('Wants=network-online.target mihomo.service');

    for (const file of [
      'cpamp-usage-persist.service',
      'cpamp-usage-rollup-hourly.service',
      'cpamp-usage-rollup-daily.service',
    ]) {
      const unit = readFileSync(path.join(systemdRoot, file), 'utf8');
      expect(unit).toContain('EnvironmentFile=/etc/cpa-manager-plus-separated/worker.env');
      expect(unit).not.toContain('--pg-dsn');
      expect(unit).not.toMatch(/postgres(?:ql)?:\/\//);
    }

    const apiEnv = readFileSync(
      path.join(nativeEdgeRoot, 'config/api.env.example'),
      'utf8'
    );
    expect(apiEnv).toContain('CPAMP_CORE_PG_DSN_FILE=');
    expect(apiEnv).toContain('CPAMP_USAGE_PG_DSN_FILE=');
    expect(apiEnv).toContain('CPAMP_AUTH_PG_DSN_FILE=');
    expect(apiEnv).not.toMatch(/^CPAMP_(CORE|USAGE|AUTH)_PG_DSN=/m);

    const cpaConfig = readFileSync(
      path.join(nativeEdgeRoot, 'config/cliproxyapi.config.example.yaml'),
      'utf8'
    );
    expect(cpaConfig).toContain('host: 127.0.0.1');
    expect(cpaConfig).toContain('proxy-url: http://127.0.0.1:7890');
  });

  it('repairs malformed auth proxy overrides without exposing or discarding tokens', () => {
    const pythonAvailable = run('python3', ['--version']);
    if (pythonAvailable.status !== 0) return;

    const authDir = tempDir('cpamp-auth-audit-');
    const invalidPath = path.join(authDir, 'invalid.json');
    const validPath = path.join(authDir, 'valid.json');
    writeFileSync(
      invalidPath,
      JSON.stringify({ access_token: 'never-print-this-token', proxy_url: '3', type: 'codex' })
    );
    writeFileSync(
      validPath,
      JSON.stringify({ access_token: 'also-private', proxy_url: 'socks5://127.0.0.1:7890' })
    );
    chmodSync(invalidPath, 0o644);
    chmodSync(validPath, 0o600);

    const script = path.join(scriptsRoot, 'cpa-auth-proxy-audit');
    const audit = run('python3', [script, '--auth-dir', authDir]);
    expect(audit.status).toBe(1);
    expect(audit.stdout).toContain('status=invalid_proxy_override');
    expect(audit.stdout).toContain('status=permissions_too_open');
    expect(audit.stdout).not.toContain('never-print-this-token');
    expect(audit.stdout).not.toContain('socks5://');

    const fix = run('python3', [script, '--auth-dir', authDir, '--fix']);
    expect(fix.status, fix.stderr).toBe(0);
    expect(fix.stdout).toContain('status=repaired');
    expect(fix.stdout).not.toContain('never-print-this-token');

    const repaired = JSON.parse(readFileSync(invalidPath, 'utf8'));
    expect(repaired.access_token).toBe('never-print-this-token');
    expect(repaired).not.toHaveProperty('proxy_url');
    expect(statSync(invalidPath).mode & 0o777).toBe(0o600);

    const valid = JSON.parse(readFileSync(validPath, 'utf8'));
    expect(valid.proxy_url).toBe('socks5://127.0.0.1:7890');
    expect(run('python3', [script, '--auth-dir', authDir]).status).toBe(0);
  });

  it('restores IPv4/IPv6 web ingress before a cloud reject chain idempotently', () => {
    if (process.platform === 'win32') return;

    const root = tempDir('cpamp-firewall-');
    const fakeBin = path.join(root, 'bin');
    const stateDir = path.join(root, 'state');
    mkdirSync(fakeBin);
    mkdirSync(stateDir);

    const fakeFirewall = `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
const name = path.basename(process.argv[1]);
const statePath = path.join(process.env.FIREWALL_STATE_DIR, name + '.json');
const initial = name === 'iptables'
  ? { chains: { INPUT: ['-j YJ-FIREWALL-INPUT'], 'YJ-FIREWALL-INPUT': ['-s 203.0.113.9/32 -j REJECT --reject-with icmp-port-unreachable'] } }
  : { chains: { INPUT: [] } };
const state = fs.existsSync(statePath) ? JSON.parse(fs.readFileSync(statePath, 'utf8')) : initial;
const save = () => fs.writeFileSync(statePath, JSON.stringify(state));
let args = process.argv.slice(2);
if (args[0] === '-w') args = args.slice(2);
const operation = args.shift();
const chain = args[0];
const fail = () => process.exit(1);
if (operation === '-S') {
  if (chain) {
    if (!Object.hasOwn(state.chains, chain)) fail();
    if (chain !== 'INPUT') process.stdout.write('-N ' + chain + '\\n');
    for (const rule of state.chains[chain]) process.stdout.write('-A ' + chain + ' ' + rule + '\\n');
  } else {
    process.stdout.write('-P INPUT ACCEPT\\n');
    for (const item of Object.keys(state.chains)) if (item !== 'INPUT') process.stdout.write('-N ' + item + '\\n');
    for (const [item, rules] of Object.entries(state.chains)) {
      for (const rule of rules) process.stdout.write('-A ' + item + ' ' + rule + '\\n');
    }
  }
  process.exit(0);
}
if (operation === '-N') {
  if (Object.hasOwn(state.chains, chain)) fail();
  state.chains[chain] = [];
  save();
  process.exit(0);
}
if (!Object.hasOwn(state.chains, chain)) fail();
args.shift();
if (operation === '-F') {
  state.chains[chain] = [];
} else if (operation === '-C') {
  process.exit(state.chains[chain].includes(args.join(' ')) ? 0 : 1);
} else if (operation === '-A') {
  state.chains[chain].push(args.join(' '));
} else if (operation === '-D') {
  const index = state.chains[chain].indexOf(args.join(' '));
  if (index < 0) fail();
  state.chains[chain].splice(index, 1);
} else if (operation === '-I') {
  const position = Number(args.shift());
  state.chains[chain].splice(Math.max(0, position - 1), 0, args.join(' '));
} else {
  fail();
}
save();
`;
    for (const name of ['iptables', 'ip6tables']) {
      const executable = path.join(fakeBin, name);
      writeFileSync(executable, fakeFirewall);
      chmodSync(executable, 0o755);
    }

    const script = path.join(scriptsRoot, 'cpa-web-ingress-allow');
    const env = {
      ...process.env,
      CPA_SYSTEM_PATH: `${fakeBin}:/usr/bin:/bin`,
      FIREWALL_STATE_DIR: stateDir,
    };
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const result = run('sh', [script], { env });
      expect(result.status, `${result.stdout}\n${result.stderr}`).toBe(0);
    }

    const readState = (name) =>
      JSON.parse(readFileSync(path.join(stateDir, `${name}.json`), 'utf8'));
    let ipv4 = readState('iptables');
    let ipv6 = readState('ip6tables');
    expect(ipv4.chains.INPUT[0]).toBe('-j CPA-WEB-INGRESS');
    expect(ipv4.chains.INPUT.filter((rule) => rule === '-j CPA-WEB-INGRESS')).toHaveLength(1);
    expect(ipv4.chains['CPA-WEB-INGRESS']).toEqual([
      '-p tcp -m multiport --dports 80,443 -j ACCEPT',
      '-j RETURN',
    ]);
    expect(ipv4.chains['YJ-FIREWALL-INPUT'][0]).toBe(
      '-p tcp -m multiport --dports 80,443 -j ACCEPT'
    );
    expect(ipv4.chains['YJ-FIREWALL-INPUT']).toContain(
      '-s 203.0.113.9/32 -j REJECT --reject-with icmp-port-unreachable'
    );
    expect(ipv6.chains.INPUT[0]).toBe('-j CPA-WEB-INGRESS');

    ipv4.chains.INPUT = ['-j YJ-FIREWALL-INPUT', '-j CPA-WEB-INGRESS'];
    ipv4.chains['YJ-FIREWALL-INPUT'] = ipv4.chains['YJ-FIREWALL-INPUT'].filter(
      (rule) => !rule.includes('--dports 80,443')
    );
    writeFileSync(path.join(stateDir, 'iptables.json'), JSON.stringify(ipv4));

    const repair = run('sh', [script], { env });
    expect(repair.status, `${repair.stdout}\n${repair.stderr}`).toBe(0);
    ipv4 = readState('iptables');
    ipv6 = readState('ip6tables');
    expect(ipv4.chains.INPUT[0]).toBe('-j CPA-WEB-INGRESS');
    expect(ipv4.chains.INPUT.filter((rule) => rule === '-j CPA-WEB-INGRESS')).toHaveLength(1);
    expect(ipv4.chains['YJ-FIREWALL-INPUT'][0]).toContain('--dports 80,443');
    expect(ipv6.chains.INPUT.filter((rule) => rule === '-j CPA-WEB-INGRESS')).toHaveLength(1);
  });

  it('creates and validates a private dump for every configured database', () => {
    if (process.platform === 'win32') return;

    const root = tempDir('cpamp-backup-');
    const fakeBin = path.join(root, 'bin');
    const backupDir = path.join(root, 'backups');
    mkdirSync(fakeBin);
    mkdirSync(backupDir);

    const fakePgDump = `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
const file = args.find((arg) => arg.startsWith('--file=')).slice('--file='.length);
const database = args.find((arg) => arg.startsWith('--dbname=')).slice('--dbname='.length);
fs.writeFileSync(file, 'fake-custom-dump:' + database);
`;
    const fakePgRestore = `#!/usr/bin/env node
const fs = require('node:fs');
const file = process.argv.at(-1);
process.exit(fs.readFileSync(file, 'utf8').startsWith('fake-custom-dump:') ? 0 : 1);
`;
    for (const [name, source] of [
      ['pg_dump', fakePgDump],
      ['pg_restore', fakePgRestore],
    ]) {
      const executable = path.join(fakeBin, name);
      writeFileSync(executable, source);
      chmodSync(executable, 0o755);
    }

    const script = path.join(scriptsRoot, 'cpamp-postgres-backup');
    const result = run('bash', [script], {
      env: {
        ...process.env,
        PATH: `${fakeBin}:${process.env.PATH || ''}`,
        CPAMP_BACKUP_DIR: backupDir,
        CPAMP_BACKUP_DATABASES: 'cpamp_usage sub2api',
        CPAMP_BACKUP_RETENTION_DAYS: '14',
      },
    });
    expect(result.status, `${result.stdout}\n${result.stderr}`).toBe(0);

    const dumps = readdirSync(backupDir).sort();
    expect(dumps).toHaveLength(2);
    expect(dumps[0]).toMatch(/^cpamp_usage\.\d{8}T\d{6}Z\.dump$/);
    expect(dumps[1]).toMatch(/^sub2api\.\d{8}T\d{6}Z\.dump$/);
    for (const dump of dumps) {
      expect(lstatSync(path.join(backupDir, dump)).isFile()).toBe(true);
      expect(statSync(path.join(backupDir, dump)).mode & 0o777).toBe(0o600);
    }

    const rejected = run('bash', [script], {
      env: {
        ...process.env,
        PATH: `${fakeBin}:${process.env.PATH || ''}`,
        CPAMP_BACKUP_DIR: backupDir,
        CPAMP_BACKUP_DATABASES: 'bad-name',
      },
    });
    expect(rejected.status).toBe(2);
    expect(rejected.stderr).toContain('invalid database name');
  });
});
