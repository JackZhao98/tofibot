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

// Where the whole cat (body, ears and tail, measured on the drawn artwork) sits in the 320-unit viewBox. A silhouette is not centred in its box:
// a loaf rests low, a tall cat leans right. A tile that holds the whole cat shifts it by `catCentre` so the cat sits in the middle of the tile.
const CENTRE={curl:{cx:160,cy:160},loaf:{cx:161,cy:187},tall:{cx:176,cy:161},rice:{cx:156,cy:181},bean:{cx:151,cy:162},puddle:{cx:159,cy:212},stand:{cx:165,cy:168}};
/** The shift that puts the cat's bounding box in the middle of its host, as multiples of the host edge (apply as translate). */
export function catCentre(shape){
  const c=CENTRE[shape];
  if(!c)throw new Error(`Unknown cat shape: ${shape}`);
  return {x:(160-c.cx)/320,y:(160-c.cy)/320};
}
