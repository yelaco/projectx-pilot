#!/usr/bin/env python3
"""Provision an exact-email Access app. Secrets are read from private files only.
Does not configure DNS, start a connector, or access a trading API.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import stat
import sys
import urllib.error
import urllib.request


def private_read(path):
    path = Path(path).expanduser()
    st = path.stat()
    if not stat.S_ISREG(st.st_mode) or st.st_uid != os.getuid() or st.st_mode & 0o077:
        raise ValueError('Credential file must be owned by you with mode 600')
    if st.st_size > 16384:
        raise ValueError('Credential file exceeds size limit')
    return path.read_text().strip()


class API:
    def __init__(self, token, account):
        self.token, self.account = token, account

    def call(self, method, suffix, body=None):
        request = urllib.request.Request(
            'https://api.cloudflare.com/client/v4/accounts/' + self.account + '/' + suffix,
            data=None if body is None else json.dumps(body).encode(), method=method,
            headers={'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'})
        # Do not follow redirects carrying credentials; do not print API bodies.
        class NoRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, *args, **kwargs):
                return None
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        try:
            with opener.open(request, timeout=15) as response:
                raw = response.read(1048577)
            if len(raw) > 1048576:
                raise ValueError('Cloudflare response exceeds limit')
            data = json.loads(raw)
            if not data.get('success'):
                raise ValueError('Cloudflare rejected ' + method + ' ' + suffix)
            return data['result']
        except urllib.error.HTTPError as exc:
            if method == 'GET' and suffix == 'access/organizations':
                if exc.code == 404:
                    return None
                if exc.code == 403:
                    error = json.loads(exc.read(65536))
                    if any('access.api.error.not_enabled' in item.get('message', '') for item in error.get('errors', [])):
                        return None
            raise ValueError('Cloudflare rejected ' + method + ' ' + suffix + ' (HTTP ' + str(exc.code) + ')') from None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--hostname', required=True)
    parser.add_argument('--email', required=True)
    parser.add_argument('--team-name', default='yelaco-pilot')
    parser.add_argument('--token-file', default='~/.cloudflared/access-api-token')
    parser.add_argument('--cert-file', default='~/.cloudflared/cert.pem')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    if any(c not in 'abcdefghijklmnopqrstuvwxyz0123456789-.' for c in args.hostname) or '.' not in args.hostname:
        raise ValueError('Provide an exact lowercase hostname')
    if args.email.count('@') != 1 or any(c in args.email for c in '* \r\n'):
        raise ValueError('Provide an exact email')
    cert = private_read(args.cert_file)
    scope = json.loads(base64.b64decode(''.join(cert.splitlines()[1:-1])))
    api = API(private_read(args.token_file), scope['accountID'])
    organization = api.call('GET', 'access/organizations')
    if not organization:
        organization = api.call('POST', 'access/organizations', {
            'name': 'ProjectX Pilot', 'auth_domain': args.team_name + '.cloudflareaccess.com'})
    team = organization['auth_domain']
    # Email OTP is an explicit identity gate; it is not represented as MFA.
    providers = api.call('GET', 'access/identity_providers')
    if not any(p['type'] == 'onetimepin' for p in providers):
        api.call('POST', 'access/identity_providers', {'name': 'Email code', 'type': 'onetimepin', 'config': {}})
    apps = api.call('GET', 'access/apps?per_page=1000')
    matches = [a for a in apps if a.get('domain') == args.hostname]
    policy = {'name': 'ProjectX operator only', 'decision': 'allow', 'precedence': 1,
              'include': [{'email': {'email': args.email}}], 'exclude': [], 'require': []}
    if not matches:
        app = api.call('POST', 'access/apps', {'name': 'ProjectX Pilot dashboard', 'domain': args.hostname,
                       'type': 'self_hosted', 'session_duration': '1h', 'policies': [policy],
                       'app_launcher_visible': False})
    elif len(matches) == 1:
        app = matches[0]
        policies = api.call('GET', 'access/apps/' + app['id'] + '/policies')
        if len(policies) != 1 or policies[0].get('decision') != 'allow' or policies[0].get('include') != policy['include'] or policies[0].get('exclude') or policies[0].get('require'):
            raise ValueError('Existing Access policy differs; inspect it before changing this app')
    else:
        raise ValueError('Multiple matching Access apps; inspect Cloudflare configuration')
    result = {'Origin': 'https://' + args.hostname, 'TeamDomain': 'https://' + team,
              'Audience': app['aud'], 'Emails': [args.email]}
    output = Path(args.output).expanduser()
    output.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = output.with_name(output.name + '.tmp')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as handle:
        json.dump(result, handle, indent=2)
        handle.write('\n')
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, output)
    print('Cloudflare Access configured; local scope written to ' + str(output))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError) as error:
        # Fixed API errors or local OS messages only, never remote response bodies.
        print(str(error), file=sys.stderr)
        sys.exit(1)
