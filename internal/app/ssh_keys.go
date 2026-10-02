package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

type computerSSHKey struct {
	Name              string `json:"name"`
	Path              string `json:"path"`
	PublicKey         string `json:"public_key"`
	Fingerprint       string `json:"fingerprint"`
	HasPrivate        bool   `json:"has_private"`
	Encrypted         bool   `json:"encrypted"`
	PublicKeyVerified bool   `json:"public_key_verified"`
	Source            string `json:"source"`
}
type sshKeyOperation struct {
	source     string
	Action     string `json:"action"`
	Name       string `json:"name,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
}
type sshKeyResult struct {
	Keys      []computerSSHKey `json:"keys,omitempty"`
	Key       *computerSSHKey  `json:"key,omitempty"`
	Error     string           `json:"error,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
}

func validSSHKeyName(name string) bool {
	if !secretFileName.MatchString(name) || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".pub") {
		return false
	}
	switch name {
	case "config", "known_hosts", "known_hosts.old", "authorized_keys", "authorized_keys2", "environment", "rc":
		return false
	}
	return true
}

// The script runs only in the configured guest through the existing shell
// broker. Its only stdout is a bounded public metadata document; subprocess
// diagnostics and imported private values never enter the returned document.
const computerSSHKeyScript = `import os,sys,json,base64,subprocess,tempfile,pathlib,re,time,shutil,itertools,struct
p=json.loads(base64.b64decode(sys.argv[1]))
home=pathlib.Path(os.environ['HOME']); root=home/'.ssh'; deadline=time.monotonic()+20
reserved={'config','known_hosts','known_hosts.old','authorized_keys','authorized_keys2','environment','rc'}
def valid(n):return bool(re.fullmatch(r'[A-Za-z0-9_][A-Za-z0-9_.-]{0,79}',n)) and not n.endswith('.pub') and n not in reserved
def command(args,content=None):
 try:
  return subprocess.run(args,input=content,stdin=subprocess.DEVNULL if content is None else None,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,timeout=max(.1,min(3,deadline-time.monotonic())),check=False)
 except (OSError,subprocess.TimeoutExpired):return None
def fingerprint(path):
 r=command(['ssh-keygen','-l','-E','sha256','-f',str(path)])
 if r is None or r.returncode:return ''
 fields=r.stdout.decode('utf-8','replace').split()
 return fields[1] if len(fields)>1 and fields[1].startswith('SHA256:') else ''
def fingerprint_data(content):
 r=command(['ssh-keygen','-l','-E','sha256','-f','-'],content)
 if r is None or r.returncode:return ''
 fields=r.stdout.decode('utf-8','replace').split()
 return fields[1] if len(fields)>1 and fields[1].startswith('SHA256:') else ''
def encrypted_public(material):
 try:
  if not material.startswith(b'-----BEGIN OPENSSH PRIVATE KEY-----'):return ''
  raw=base64.b64decode(b''.join(material.splitlines()[1:-1]),validate=True)
  magic=b'openssh-key-v1\x00'
  if not raw.startswith(magic):return ''
  offset=len(magic)
  def field():
   nonlocal offset
   length=struct.unpack('>I',raw[offset:offset+4])[0];offset+=4
   value=raw[offset:offset+length];offset+=length
   if len(value)!=length:raise ValueError()
   return value
  cipher=field();field();field()
  count=struct.unpack('>I',raw[offset:offset+4])[0];offset+=4
  if cipher==b'none' or count!=1:return ''
  blob=field();encrypted=field()
  if not encrypted or offset!=len(raw):return ''
  size=struct.unpack('>I',blob[:4])[0];kind=blob[4:4+size].decode('ascii')
  value=kind+' '+base64.b64encode(blob).decode('ascii')
  return value if fingerprint_data(value.encode()) else ''
 except (ValueError,UnicodeError,struct.error):return ''
def public(path):
 if path.is_symlink() or not path.is_file() or path.stat().st_size>16384:return ''
 content=path.read_text(errors='replace').strip()
 if len(content.splitlines())!=1 or not re.match(r'^(ssh-|ecdsa-|sk-)[A-Za-z0-9@._+-]+ [A-Za-z0-9+/=]+(?: .*)?$',content):return ''
 return content if fingerprint(path) else ''
def inspect(path):
 if path.is_symlink() or not path.is_file() or path.stat().st_size>65536:return None
 with path.open('rb') as f:material=f.read(65537)
 if len(material)>65536 or b'PRIVATE KEY-----' not in material[:100]:return None
 r=command(['ssh-keygen','-y','-P','','-f',str(path)])
 derived=r.stdout.decode('utf-8','replace').strip() if r is not None and r.returncode==0 else ''
 header_public=encrypted_public(material) if not derived else ''
 fp=fingerprint_data((derived or header_public).encode()) if derived or header_public else ''
 if not fp:return None
 # An encrypted key cannot prove its private material matches a supplied .pub.
 pub=derived or header_public or public(path.with_name(path.name+'.pub'))
 if not derived and not header_public and pub and fingerprint(path.with_name(path.name+'.pub'))!=fp:pub=''
 if pub and not re.match(r'^(ssh-|ecdsa-|sk-)',pub):pub=''
 return {'name':path.name,'path':str(path),'public_key':pub,'fingerprint':fp,'has_private':True,'encrypted':not bool(derived),'public_key_verified':bool(derived),'source':'computer'}
def fail(code):print(json.dumps({'error':code}));sys.exit(0)
try:
 if not shutil.which('ssh-keygen'):fail('ssh_unavailable')
 if root.is_symlink():fail('unsafe_directory')
 if p['action']=='list':
  keys=[];truncated=False
  if root.is_dir():
   entries=list(itertools.islice(root.iterdir(),257));truncated=len(entries)>256
   for path in sorted(entries[:256],key=lambda x:x.name):
    if time.monotonic()>deadline or len(keys)>=64:truncated=True;break
    try:
     if path.name.endswith('.pub') and valid(path.name[:-4]) and not os.path.lexists(root/path.name[:-4]):
      pub=public(path)
      if pub:keys.append({'name':path.name[:-4],'path':str(path),'public_key':pub,'fingerprint':fingerprint(path),'has_private':False,'encrypted':False,'public_key_verified':False,'source':'computer'})
      continue
     if not valid(path.name):continue
     item=inspect(path)
     if item:keys.append(item)
    except (OSError,UnicodeError,ValueError):continue
  print(json.dumps({'keys':keys,'truncated':truncated}));sys.exit(0)
 name=p.get('name','')
 if not valid(name):fail('invalid_name')
 root.mkdir(mode=0o700,parents=True,exist_ok=True);root.chmod(0o700)
 destination=root/name;pubdestination=root/(name+'.pub')
 if os.path.lexists(destination) or os.path.lexists(pubdestination):fail('already_exists')
 with tempfile.TemporaryDirectory(prefix='.tofi-key-',dir=root) as tmp:
  temporary=pathlib.Path(tmp)/name
  if p['action']=='generate':
   r=command(['ssh-keygen','-q','-t','ed25519','-N','','-C','tofi','-f',str(temporary)])
   if r is None or r.returncode:fail('generation_failed')
  elif p['action']=='import':
   value=p.get('private_key','')
   if not value or len(value.encode())>65536 or '\x00' in value:fail('invalid_private_key')
   temporary.write_text(value.rstrip()+'\n');temporary.chmod(0o600)
   supplied=p.get('public_key','').strip()
   if supplied:
    temporary.with_name(name+'.pub').write_text(supplied+'\n')
    if not public(temporary.with_name(name+'.pub')):fail('invalid_public_key')
  else:fail('invalid_action')
  item=inspect(temporary)
  if not item:fail('invalid_private_key')
  if item['public_key_verified']:
   temporary.with_name(name+'.pub').write_text(item['public_key']+'\n')
  elif p.get('public_key') and fingerprint(temporary.with_name(name+'.pub'))!=item['fingerprint']:fail('public_key_mismatch')
  if item['public_key'] and not item['public_key_verified']:temporary.with_name(name+'.pub').write_text(item['public_key']+'\n')
  installed=[]
  try:
   for src,dst,mode in [(temporary,destination,0o600),(temporary.with_name(name+'.pub'),pubdestination,0o644)]:
    if not src.exists():continue
    fd=os.open(dst,os.O_WRONLY|os.O_CREAT|os.O_EXCL,mode);installed.append(dst)
    with os.fdopen(fd,'wb') as f:f.write(src.read_bytes());f.flush();os.fsync(f.fileno())
  except FileExistsError:
   for path in installed:path.unlink()
   fail('already_exists')
  except OSError:
   for path in installed:path.unlink()
   fail('write_failed')
  item['path']=str(destination)
  print(json.dumps({'key':item}))
except Exception:fail('operation_failed')
`

func (s *Server) computerSSHKeys(ctx context.Context, r Run, op sshKeyOperation) (sshKeyResult, error) {
	if op.Action != "list" && op.Action != "generate" && op.Action != "import" {
		return sshKeyResult{}, tooloutcome.InvalidArguments("Unsupported SSH key action")
	}
	if op.Action != "list" && !validSSHKeyName(op.Name) {
		return sshKeyResult{}, tooloutcome.InvalidArguments("Choose a private key filename such as id_ed25519_work")
	}
	if op.Action == "import" && (!validSecretValue(op.PrivateKey) || len(op.PublicKey) > 16384) {
		return sshKeyResult{}, tooloutcome.InvalidArguments("A valid private key is required")
	}
	data, _ := json.Marshal(op)
	args, _ := json.Marshal(map[string]any{"command": "python3 -c " + secretQuote(computerSSHKeyScript) + " " + secretQuote(base64.StdEncoding.EncodeToString(data)), "timeout_sec": 25})
	var output string
	var err error
	if op.Action == "list" {
		source := op.source
		if source == "" {
			source = "model"
		}
		output, err = s.microVMActionFromSource(ctx, r, "shell.exec", args, source)
	} else {
		output, err = s.microVMAction(ctx, r, "shell.exec", args)
	}
	if err != nil {
		return sshKeyResult{}, errors.New("Computer unavailable or busy; try again after its current task")
	}
	var shell struct {
		Stdout    string `json:"stdout"`
		ExitCode  *int   `json:"exit_code"`
		Truncated bool   `json:"truncated"`
	}
	if json.Unmarshal([]byte(output), &shell) != nil || shell.ExitCode == nil || *shell.ExitCode != 0 || shell.Truncated {
		return sshKeyResult{}, errors.New("Could not read SSH keys from the computer")
	}
	var result sshKeyResult
	if json.Unmarshal([]byte(shell.Stdout), &result) != nil {
		return result, errors.New("Invalid SSH key response")
	}
	if result.Error != "" {
		messages := map[string]string{"already_exists": "This key filename already exists; choose another name", "invalid_private_key": "The private key could not be parsed by OpenSSH", "invalid_public_key": "The public key is invalid", "public_key_mismatch": "The supplied public key does not match the key fingerprint", "ssh_unavailable": "OpenSSH tools are not installed on the computer", "unsafe_directory": "The SSH directory must be a regular directory"}
		if msg := messages[result.Error]; msg != "" {
			return sshKeyResult{}, errors.New(msg)
		}
		return sshKeyResult{}, errors.New("SSH key operation failed")
	}
	return result, nil
}
func (s *Server) handleSSHKeys(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/computer/ssh-keys" {
		return false
	}
	op := sshKeyOperation{Action: "list"}
	botID := r.URL.Query().Get("bot_id")
	if r.Method == "POST" {
		var in struct {
			BotID string `json:"bot_id"`
			sshKeyOperation
		}
		if decode(r, &in) != nil {
			writeErr(w, 400, "ssh_keys", "Invalid key request")
			return true
		}
		botID = in.BotID
		op = in.sshKeyOperation
		if op.Action != "generate" && op.Action != "import" {
			writeErr(w, 400, "ssh_keys", "Choose generate or import")
			return true
		}
	} else if r.Method != "GET" {
		writeErr(w, 405, "ssh_keys", "Method not allowed")
		return true
	}
	if r.Method == "POST" && (!validSSHKeyName(op.Name) || (op.Action == "import" && (!validSecretValue(op.PrivateKey) || len(op.PublicKey) > 16384))) {
		writeErr(w, 400, "ssh_keys", "请检查密钥文件名与内容格式")
		return true
	}
	if _, err := s.store.GetBot(botID); err != nil {
		writeErr(w, 404, "ssh_keys", "Bot not found")
		return true
	}
	if r.Method == "POST" && s.botHasActiveRun(botID) {
		writeErr(w, 409, "ssh_keys", "Wait for this Bot's current task to finish")
		return true
	}
	op.source = "human"
	run := Run{ID: "ssh-settings-" + uuid.NewString(), BotID: botID}
	defer s.releaseComputerOwner(run.BotID, run.ID)
	result, err := s.computerSSHKeys(r.Context(), run, op)
	if err != nil {
		writeErr(w, 409, "ssh_keys", err.Error())
		return true
	}
	if op.Action == "list" && result.Keys == nil {
		result.Keys = []computerSSHKey{}
	}
	if op.Action == "list" {
		writeJSON(w, 200, map[string]any{"keys": result.Keys, "truncated": result.Truncated})
	} else {
		writeJSON(w, 200, result)
	}
	return true
}
func (s *Server) sshKeyTool(r Run) Tool {
	return Tool{Name: "computer_ssh_keys", Description: "List or generate SSH keys in the shared computer HOME ~/.ssh. Settings discovers these same keys automatically. Returns public keys and fingerprints only, never private material. Generate creates a new unencrypted Ed25519 key without overwriting existing files; only do so when the user requests a key. For private-key import use request_secret_input then use_secret_input(action ssh,target filename), never ask for a private key in chat. A public key must still be added to GitHub or the intended remote service by the user or an explicitly authorized action.", Parameters: objectSchema(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"list", "generate"}}, "name": map[string]any{"type": "string", "description": "Private key filename, for example id_ed25519_work"}}, []string{"action"}), Identity: func(raw json.RawMessage) tooloutcome.Identity {
		var in sshKeyOperation
		_ = json.Unmarshal(raw, &in)
		i := tooloutcome.OperationIdentity("computer/ssh_keys", in.Action, raw)
		if in.Action == "list" {
			i.Risk = tooloutcome.Observation
		}
		if in.Action == "generate" {
			i.Risk = tooloutcome.TargetMutation
			i.Target = in.Name
		}
		return i
	}, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var op sshKeyOperation
		if json.Unmarshal(raw, &op) != nil || (op.Action != "list" && op.Action != "generate") || op.PrivateKey != "" || op.PublicKey != "" {
			return "", tooloutcome.InvalidArguments("Choose list or generate; private values must use Secret Input")
		}
		result, err := s.computerSSHKeys(ctx, r, op)
		if err != nil {
			return "", err
		}
		if op.Action == "list" {
			data, _ := json.Marshal(map[string]any{"keys": result.Keys, "truncated": result.Truncated})
			return string(data), nil
		}
		data, _ := json.Marshal(result)
		return string(data), nil
	}}
}
