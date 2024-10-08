document.addEventListener("DOMContentLoaded", init);
const raf = window.requestAnimationFrame;

function init() {
  const canvas = document.getElementById("canvas");
  const ctx = canvas.getContext("2d");
  const data = {
    ctx,
    // speed of movement (higher = faster)
    speed: 200,
    frame: 1,
    prev: 0,
    cur: 0,
    // boundary balls can traverse inside canvas
    minY: -20,
    maxY: canvas.height + 20,
    // balls
    entities: [
      { x: 20, y: 0, mod: 1 },
      // { x: 25, y: canvas.height / 2, mod: 1 },
      { x: 35, y: canvas.height, mod: -1 },
    ],
  };

  raf(tick(data));
}

function tick(data) {
  return (ms) => {
    const prev = data.cur;
    const cur = ms * 0.001;
    const next = {
      ...data,
      prev,
      cur,
      frame: data.frame + 1,
      delta: cur - prev,
    };
    return loop(next);
  };
}

function loop(data) {
  const { ctx, entities, speed, delta, minY, maxY } = data;
  const width = ctx.canvas.width;
  const height = ctx.canvas.height;
  ctx.clearRect(0, 0, width, height);

  for (let i = 0; i < entities.length; i += 1) {
    const entity = entities[i];
    const x = entity.x;
    const y = entity.y + (speed * delta * entity.mod);
    const nextMod = () => {
      if (y < minY) return 1;
      if (y > maxY) return -1;
      return entity.mod;
    };

    // grace for entire ball to leave scene
    // const buffer = 25
    // dont bother drawing circle if outside canvas
    // if (y + buffer >= 0 && y - buffer <= height) {
    ball(ctx, x, y);
    // }

    entities[i].x = x;
    entities[i].y = y;
    entities[i].mod = nextMod();
  }

  raf(tick(data));
}

function ball(ctx, x, y) {
  ctx.fillStyle = "#f2f2f2";
  ctx.beginPath();
  ctx.arc(x, y, 5, 0, 2 * Math.PI);
  ctx.fill();
}
