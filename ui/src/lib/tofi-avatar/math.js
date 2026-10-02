export const clamp=(n,a=0,b=1)=>Math.max(a,Math.min(b,n));
export const smooth=(a,b,x)=>{const u=clamp((x-a)/(b-a));return u*u*(3-2*u);};
export const ease=t=>t*t*t*(t*(t*6-15)+10);
export function rotate(x,y,cx,cy,degrees){
  const r=degrees*Math.PI/180,c=Math.cos(r),s=Math.sin(r);
  return [cx+(x-cx)*c-(y-cy)*s,cy+(x-cx)*s+(y-cy)*c];
}
