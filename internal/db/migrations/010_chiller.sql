-- A chiller is the load at the foot of a way rather than another board: the
-- conductor runs from its switch straight into the machine, so it terminates
-- the way the way a motor terminates a circuit.

ALTER TYPE hv_device_kind ADD VALUE IF NOT EXISTS 'chiller';
