Here is a comprehensive specification and design document tailored for an AI coder or developer to implement a single-player, bot-driven **Agar.io clone** using standard game loops (e.g., Python/Pygame, Go with `go-sdl3`, or JavaScript Canvas).

---

## 1. System Overview & Core Architecture

The game is a top-down, multi-entity 2D arena where the player controls one or more "cells" that grow by consuming passive food pellets or smaller opponent cells, while avoiding larger entities and hazard viruses.

### Standard Entity Hierarchy

1. **World / Arena:** Bounded 2D plane (e.g., $4000 \times 4000$ spatial units).
2. **Food (Pellets):** Static, non-moving passive entities with fixed mass.
3. **Cells (Player & Bots):** Dynamic circles with variable mass, velocity, color, and unique IDs.
4. **Viruses:** Static green spiked entities that split cells above a mass threshold upon collision.

---

## 2. Core Game Physics & Mechanics

### Mass & Radius Relationship

A cell's visual size is determined by its mass $M$.


$$\text{Radius } R = \sqrt{M \cdot C_r} \quad (\text{where } C_r \approx 100 \text{ scale factor})$$

### Speed / Mass Scaling

Movement speed $S$ scales inversely with mass so larger cells move slower:


$$S = \max\left(S_{\text{min}}, \frac{K_s}{M^{\gamma}}\right)$$

* *Suggested Defaults:* Base speed $K_s = 2.2$, exponent $\gamma \approx 0.44$, min speed limit $S_{\text{min}} = 1.0$.

### Mass Decay

To prevent infinite growth, cells above a threshold mass $M_{\text{decay\_min}}$ lose a small percentage of mass per tick:


$$M_{\text{new}} = M \cdot (1 - \text{decay\_rate})$$

* *Suggested Defaults:* $M_{\text{decay\_min}} = 100$, $\text{decay\_rate} = 0.002\% \text{ per tick}$.

---

## 3. Player & Entity Actions

```
               [ Input Target (Mouse / Direction Vector) ]
                                   │
                                   ▼
                       [ Cell Velocity Vector ]
                                   │
         ┌─────────────────────────┼─────────────────────────┐
         ▼                         ▼                         ▼
   [ Standard Move ]      [ Split (Space) ]        [ Eject Mass (W) ]
   Gradual steering       Spawns 2nd cell towards  Ejects passive pellet
   towards target         target; propels forward  forward; reduces mass

```

### Movement

* **Direction Vector:** Cell accelerates smoothly toward target coordinates $(T_x, T_y)$ relative to screen space.
* **Mass Center:** If a player controls multiple split sub-cells, the camera centers on their average center of mass.

### Action A: Eject Mass (`W` key / Action Trigger)

* **Pre-condition:** Cell mass $M \ge M_{\text{eject\_min}}$ (e.g., $35$).
* **Effect:** Cell reduces mass by $M_{\text{eject\_cost}}$ ($16$ mass) and spawns a small, fast-moving "Ejected Mass" pellet moving in the direction of the target point.
* **Velocity:** High initial impulse velocity that rapidly decays to $0$.

### Action B: Split (`Space` key / Action Trigger)

* **Pre-condition:** Cell mass $M \ge M_{\text{split\_min}}$ (e.g., $36$) AND total sub-cells controlled by player $< \text{MaxCells}$ (e.g., 16).
* **Effect:** Split cell into two equal cells ($M_{\text{new}} = M / 2$).
* **Impulse:** The newly created sub-cell gains a temporary forward speed boost directed toward target coordinates.
* **Recombine Timer:** Sub-cells belonging to the same player cannot recombine immediately. They track a cooldown timer:

$$\text{Cooldown (seconds)} = T_{\text{base}} + (M \cdot \alpha) \quad (\text{e.g., } 30\text{s} + M \times 0.02)$$



---

## 4. Entity Collisions & Interactions

At every tick, run spatial partitioning (Quadtree or Grid) for fast broad-phase collision detection:

| Primary Entity | Secondary Entity | Collision Condition | Outcome |
| --- | --- | --- | --- |
| **Cell A** | **Food Pellet** | Distance $\le R_A$ | Cell A eats pellet. $M_A \mathrel{+}= M_{\text{food}}$. Spawn replacement food. |
| **Cell A** | **Cell B (Enemy)** | Distance $\le (R_A - 0.33 \cdot R_B)$ AND $M_A \ge 1.25 \cdot M_B$ | Cell A completely consumes Cell B. $M_A \mathrel{+}= M_B$. Cell B despawns/respawns. |
| **Cell A** | **Virus** | Distance $\le (R_A + R_{\text{virus}})$ AND $M_A > M_{\text{virus\_threshold}}$ | Cell A hits virus: split Cell A into maximum possible sub-cells (up to 16). Virus despawns or respawns. |
| **Ejected Mass** | **Virus** | Distance $\le (R_{\text{eject}} + R_{\text{virus}})$ | Virus absorbs mass. After absorbing $N$ ejected masses ($7$ typical), virus splits and shoots a new virus in the ejected vector's direction. |
| **Sub-cell A** | **Sub-cell B** *(Same Player)* | Cooldown expired & overlapping | Cells merge into a single cell with mass $M_{A} + M_{B}$. |
| **Sub-cell A** | **Sub-cell B** *(Same Player)* | Cooldown active & overlapping | Apply soft elastic collision / push force so sub-cells do not overlap completely. |

---

## 5. AI Bot Logic (Single-Player Engine)

Run a high-frequency tick routine for each AI bot to generate virtual target coordinates $(T_x, T_y)$ and action triggers:

1. **State Evaluation:** Scan local vision radius $R_{\text{vision}} = \text{Radius} \cdot K_{\text{fov}}$.
2. **Threat Avoidance:** If any opponent cell has $M_{\text{enemy}} > 1.25 \cdot M_{\text{bot}}$ within threat distance, set target vector directly **away** from threat.
3. **Prey Hunting:** If an opponent cell has $M_{\text{enemy}} < 0.8 \cdot M_{\text{bot}}$ within range, set target vector directly **toward** prey.
* *Aggressive Split:* If $M_{\text{bot}} > 2 \cdot M_{\text{enemy}}$ and distance is short, trigger **Split** action toward prey.


4. **Food Harvesting:** If no threats or prey are nearby, target the highest density cluster of passive food pellets or closest ejected mass.
5. **Virus Avoidance:** If $M_{\text{bot}} > M_{\text{virus}}$, treat viruses as static obstacles with higher repulsive weight.

---

## 6. Data Structures & Loop Pseudo-Implementation

```python
class Cell:
    id: str
    owner_id: str
    x: float
    y: float
    mass: float
    vx: float
    vy: float
    recombine_timer: float
    color: Tuple[int, int, int]

    @property
    def radius(self) -> float:
        return math.sqrt(self.mass * 100)

    @property
    def speed(self) -> float:
        return max(1.0, 2.2 / (self.mass ** 0.44))

class WorldState:
    width: float = 4000.0
    height: float = 4000.0
    food_list: List[Food]
    viruses: List[Virus]
    cells: List[Cell]
    
    def step(self, dt: float):
        self.update_ai_decisions()
        self.apply_movement(dt)
        self.resolve_collisions()
        self.apply_mass_decay(dt)
        self.maintain_food_and_bot_counts()

```

---

## 7. Rendering & Viewport Mechanics

* **Camera / Viewport:** Scale viewport dimensions dynamically based on player size so larger masses zoom out smooth camera transforms:

$$\text{Zoom Level} = \max\left(Z_{\text{min}}, \frac{K_z}{\sqrt{\text{Total Player Mass}}}\right)$$


* **World Grid:** Render a subtle background grid pattern to provide motion feedback.
* **Z-Index Rendering Order:**
1. Background Grid & Arena Boundaries
2. Passive Food Pellets & Ejected Mass
3. Viruses (Green, spiky edges)
4. Player & Bot Sub-cells (Lower mass to higher mass)
5. Player Names & Mass Overlay Labels
