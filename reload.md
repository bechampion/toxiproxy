# Dynamic conf and reload:


Proxy start with this conf file [proxy.conf](./proxy.conf) that looks like:
```
[
  {
    "name": "baba",
    "listen": "[::]:8881",
    "upstream": "www.google.com:80",
    "enabled": true
  },
  {
    "name": "example",
    "listen": "[::]:8888",
    "upstream": "www.google.com:80",
    "enabled": true
  }
]

```


And as you can see the loaded proxies look like:
[proxies](./img/allproxies.png)


## Removing a proxy:
Conf file now looks like:
```
[
  {
    "name": "example",
    "listen": "[::]:8888",
    "upstream": "www.google.com:80",
    "enabled": true
  }
]

```

And the element is removed from the conf :
[modifiedproxies](./img/lessproxies.png)






