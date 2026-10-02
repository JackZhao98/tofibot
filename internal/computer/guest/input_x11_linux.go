//go:build linux

package guest

// A foreground, session-owned X11 helper. It reports readiness after XSync,
// and paste delivery only after placing UTF-8 bytes in the requestor's property.
// The bounded payload fits one ordinary XChangeProperty request (no INCR).
// Watch mode observes XFixes selection changes, including same-owner copies.
const inputSelectionHelper = `
import ctypes as C, ctypes.util, select, sys, time
X = C.CDLL(ctypes.util.find_library('X11'))
P = C.c_void_p
U = C.c_ulong
I = C.c_int
def bind(lib, name, result, args):
    f = getattr(lib, name); f.restype = result; f.argtypes = args; return f
bind(X,'XOpenDisplay',P,[C.c_char_p])
bind(X,'XDefaultRootWindow',U,[P])
bind(X,'XCreateSimpleWindow',U,[P,U,I,I,C.c_uint,C.c_uint,C.c_uint,U,U])
bind(X,'XInternAtom',U,[P,C.c_char_p,I])
bind(X,'XSetSelectionOwner',I,[P,U,U,U])
bind(X,'XGetSelectionOwner',U,[P,U])
bind(X,'XSync',I,[P,I])
bind(X,'XConnectionNumber',I,[P])
bind(X,'XPending',I,[P])
bind(X,'XNextEvent',I,[P,P])
bind(X,'XChangeProperty',I,[P,U,U,U,I,I,P,I])
bind(X,'XSendEvent',I,[P,U,I,C.c_long,P])
bind(X,'XCloseDisplay',I,[P])
class Request(C.Structure):
    _fields_=[('type',I),('serial',U),('send_event',I),('display',P),('owner',U),('requestor',U),('selection',U),('target',U),('property',U),('time',U)]
class Notify(C.Structure):
    _fields_=[('type',I),('serial',U),('send_event',I),('display',P),('requestor',U),('selection',U),('target',U),('property',U),('time',U)]
d = X.XOpenDisplay(None)
if not d: raise RuntimeError('X display is unavailable')
try:
    w = X.XCreateSimpleWindow(d,X.XDefaultRootWindow(d),0,0,1,1,0,0,0)
    atom = lambda name: X.XInternAtom(d,name.encode(),0)
    clipboard, utf8, targets, string = [atom(name) for name in ('CLIPBOARD','UTF8_STRING','TARGETS','STRING')]
    event_base = I()
    mode = sys.argv[1]
    if mode == 'own':
        data = sys.stdin.buffer.read(65537)
        if len(data)>65536: raise RuntimeError('clipboard exceeds limit')
        X.XSetSelectionOwner(d,clipboard,w,0)
        X.XSync(d,0)
        if X.XGetSelectionOwner(d,clipboard)!=w: raise RuntimeError('clipboard ownership failed')
    else:
        fixes = C.CDLL(ctypes.util.find_library('Xfixes'))
        bind(fixes,'XFixesQueryExtension',I,[P,C.POINTER(I),C.POINTER(I)])
        bind(fixes,'XFixesSelectSelectionInput',None,[P,U,U,U])
        error_base = I()
        if not fixes.XFixesQueryExtension(d,C.byref(event_base),C.byref(error_base)): raise RuntimeError('XFixes is unavailable')
        fixes.XFixesSelectSelectionInput(d,w,clipboard,1)
        X.XSync(d,0)
    print('ready',flush=True)
    deadline = time.monotonic()+2
    while True:
        if not X.XPending(d):
            timeout = None if mode=='own' else max(0,deadline-time.monotonic())
            if not select.select([X.XConnectionNumber(d)],[],[],timeout)[0]: break
        event = (C.c_long*24)()
        X.XNextEvent(d,C.byref(event))
        kind = C.cast(event,C.POINTER(I)).contents.value
        if mode!='own':
            if kind==event_base.value:
                print('changed',flush=True); break
            continue
        if kind==29: break # SelectionClear: another app now owns the clipboard.
        if kind!=30: continue
        req = C.cast(event,C.POINTER(Request)).contents
        prop = req.property or req.target
        delivered = False
        if req.target==targets:
            values = (U*3)(targets,utf8,string)
            X.XChangeProperty(d,req.requestor,prop,4,32,0,C.cast(values,P),3)
        elif req.target in (utf8,string):
            payload = data if req.target==utf8 else data.decode('utf-8').encode('latin1','replace')
            values = C.create_string_buffer(payload)
            X.XChangeProperty(d,req.requestor,prop,req.target,8,0,C.cast(values,P),len(payload))
            delivered = True
        else:
            prop = 0
        reply = Notify(31,0,1,d,req.requestor,req.selection,req.target,prop,req.time)
        reply_event = (C.c_long*24)()
        C.memmove(C.byref(reply_event),C.byref(reply),C.sizeof(reply))
        X.XSendEvent(d,req.requestor,0,0,C.byref(reply_event))
        X.XSync(d,0)
        if delivered: print('delivered',flush=True)
finally:
    X.XCloseDisplay(d)
`
