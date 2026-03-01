import os
import re

for root, _, files in os.walk('connector'):
    for file in files:
        if file.endswith('.go'):
            path = os.path.join(root, file)
            with open(path, 'r') as f:
                content = f.read()

            # Skip if file already has the new signature (like oauth.go or authproxy.go)
            if '[]byte' in content and 'LoginURL' in content and 'HandleCallback' in content:
                # wait, authproxy test will matched, let's just do blind replace but avoid double replace
                pass

            orig_content = content

            # Replace LoginURL signatures
            content = re.sub(
                r'(func\s*\([^)]+\)\s*LoginURL\s*\([^)]+\)\s*)\(string,\s*error\)\s*\{',
                r'\1(string, []byte, error) {',
                content
            )

            # Fix returns in LoginURL
            # Replace `return oauth2Config.AuthCodeURL(state), nil`
            # We need to make sure we only replace inside LoginURL. Let's do a simple regex for returns that are `return "", fmt.Errorf`
            # and `return u.String(), nil`
            lines = content.split('\n')
            in_login_url = False
            for i, line in enumerate(lines):
                if re.search(r'func\s*\(.*?\)\s*LoginURL\(', line):
                    in_login_url = True
                elif in_login_url and line.startswith('}'):
                    in_login_url = False

                if in_login_url:
                    if re.search(r'return\s+[^,]+,\s+nil\s*$', line):
                        lines[i] = re.sub(r'(return\s+[^,]+),\s+nil\s*$', r'\1, nil, nil', line)
                    elif re.search(r'return\s+"",\s+fmt\.Errorf', line):
                        lines[i] = re.sub(r'return\s+"",\s+fmt\.Errorf', r'return "", nil, fmt.Errorf', line)
                    elif re.search(r'return\s+"",\s+errors\.New', line):
                        lines[i] = re.sub(r'return\s+"",\s+errors\.New', r'return "", nil, errors.New', line)
                    elif re.search(r'return\s+"",\s+err\s*$', line):
                        lines[i] = re.sub(r'return\s+"",\s+err\s*$', r'return "", nil, err', line)

            content = '\n'.join(lines)

            # HandleCallback signatures
            # func (c *giteaConnector) HandleCallback(s connector.Scopes, r *http.Request) (identity connector.Identity, err error) {
            content = re.sub(
                r'(func\s*\([^)]+\)\s*HandleCallback\s*\([^,]+)\s*,\s*(r\s+\*http\.Request\)\s*\([^)]+\)\s*\{)',
                r'\1, _ []byte, \2',
                content
            )

            # There might be tests that reference LoginURL or HandleCallback that need to be updated.
            # `loginURL, err := conn.LoginURL(` -> `loginURL, _, err := conn.LoginURL(`
            content = re.sub(
                r'([a-zA-Z0-9_]+),\s*err\s*(:=|=)\s*([a-zA-Z0-9_]+\.)?LoginURL\(',
                r'\1, _, err \2 \3LoginURL(',
                content
            )

            # `ident, err := conn.HandleCallback(scopes, req)` -> `ident, err := conn.HandleCallback(scopes, nil, req)`
            content = re.sub(
                r'(HandleCallback\s*\([^,]+)\s*,\s*req\)',
                r'\1, nil, req)',
                content
            )
            # if the variable is something else like r
            content = re.sub(
                r'(HandleCallback\s*\([^,]+)\s*,\s*([a-zA-Z0-9_]+)\)',
                r'\1, nil, \2)',
                content
            )

            if content != orig_content:
                with open(path, 'w') as f:
                    f.write(content)
