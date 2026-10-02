//go:build linux

package guest

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// Tint2 paints pseudo-transparent corners from the wallpaper without a
// compositor. Clip the actual window so those corners expose the application
// underneath instead. This one-shot helper uses the guest's existing X11 libs.
func shapeDesktopDock(parent context.Context, env []string, pid int) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", dockShapeHelper, strconv.Itoa(pid))
	cmd.Env = env
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("shape desktop dock: %w: %s", err, output)
	}
	return nil
}

const dockShapeHelper = `
import ctypes as C, ctypes.util, math, os, subprocess, sys, time
X=C.CDLL(ctypes.util.find_library('X11'))
E=C.CDLL(ctypes.util.find_library('Xext'))
P=C.c_void_p; U=C.c_ulong; I=C.c_int
def bind(lib,name,result,args):
    f=getattr(lib,name); f.restype=result; f.argtypes=args; return f
bind(X,'XOpenDisplay',P,[C.c_char_p])
bind(X,'XGetGeometry',I,[P,U,C.POINTER(U),C.POINTER(I),C.POINTER(I),C.POINTER(C.c_uint),C.POINTER(C.c_uint),C.POINTER(C.c_uint),C.POINTER(C.c_uint)])
bind(X,'XSync',I,[P,I]); bind(X,'XCloseDisplay',I,[P])
class Rect(C.Structure):
    _fields_=[('x',C.c_short),('y',C.c_short),('width',C.c_ushort),('height',C.c_ushort)]
bind(E,'XShapeCombineRectangles',None,[P,U,I,I,I,C.POINTER(Rect),I,I,I])
d=X.XOpenDisplay(None)
if not d: raise RuntimeError('display unavailable')
try:
    deadline=time.monotonic()+3
    while True:
        os.kill(int(sys.argv[1]),0)
        found=subprocess.run(['xdotool','search','--all','--class','^Tint2$'],capture_output=True,text=True,timeout=1)
        ids=found.stdout.split()
        if len(ids)>1: raise RuntimeError('ambiguous dock windows')
        if ids: break
        if time.monotonic()>deadline: raise RuntimeError('dock window unavailable')
        time.sleep(.03)
    w=int(ids[0]); root=U(); x=I(); y=I(); width=C.c_uint(); height=C.c_uint(); border=C.c_uint(); depth=C.c_uint()
    if not X.XGetGeometry(d,w,C.byref(root),C.byref(x),C.byref(y),C.byref(width),C.byref(height),C.byref(border),C.byref(depth)): raise RuntimeError('dock geometry unavailable')
    if not (32<=width.value<=512 and 24<=height.value<=128): raise RuntimeError('unexpected dock geometry')
    radius=min(18,height.value//2); rows=[]
    for row in range(height.value):
        edge=min(row,height.value-1-row)
        inset=math.ceil(radius-math.sqrt(radius*radius-(radius-edge-.5)**2)) if edge<radius else 0
        rows.append(Rect(inset,row,width.value-2*inset,1))
    rects=(Rect*len(rows))(*rows)
    E.XShapeCombineRectangles(d,w,0,0,0,rects,len(rows),0,0)
    X.XSync(d,0)
finally:
    X.XCloseDisplay(d)
`
