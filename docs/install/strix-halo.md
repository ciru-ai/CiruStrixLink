# AMD Strix Halo requirement

The public model package targets AMD Ryzen AI Max processors, commonly called
Strix Halo. Confirm the CPU locally:

```bash
lscpu | grep 'Model name'
```

A non-Strix system may support Linux USB4NET, but it is outside the validated
model and transport target. There is no software package that converts an
unsupported processor into a supported platform.

