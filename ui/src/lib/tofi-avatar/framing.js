// Portrait framing (host-level, additive; its own file so a bundle that never crops a head does not carry it): where the head sits in the 320-unit viewBox, so a small round or bare frame
// can show the head and crop the body. cx/cy: head centre; span: viewBox width that should fill the frame.
const FRAMING={
  curl:{cx:150,cy:150,span:200},puddle:{cx:160,cy:190,span:220},
  tall:{cx:152,cy:124,span:158},rice:{cx:160,cy:168,span:172},bean:{cx:200,cy:128,span:148},loaf:{cx:100,cy:182,span:152},stand:{cx:104,cy:162,span:148}
};
/**
 * Where to place a mounted cat inside a square frame so the head fills it and the body is cropped at the bottom edge.
 * All values are multiples of the frame edge: the 320-unit cat host should be `size` x the frame wide and tall, at
 * (`left`, `top`) from the frame's top-left. `headY` is where the head centre lands (0 top .. 1 bottom).
 */
export function catFraming(shape,headY=.52){
  const f=FRAMING[shape];
  if(!f)throw new Error(`Unknown cat shape: ${shape}`);
  return {size:320/f.span,left:.5-f.cx/f.span,top:headY-f.cy/f.span};
}
