# Xboard API 契约审计 — Part 2

审计范围：`design/Xboard-master` 下 V1 User（Plan / Order / Coupon / GiftCard）与 V1 Guest（Plan / Comm）
相关 Controller、Request、Resource。仅提取源码中真实存在的内容；凡推断均标注「推断」，未读到标注「未读取」。
本次审计只读不写，未修改任何现有源码文件。

## 全局前提（均已读源码）

| 项 | 内容 | 定位 |
| --- | --- | --- |
| 路由前缀 | V1 路由统一挂在 `/api/v1`，中间件组 `api` | `app/Providers/RouteServiceProvider.php:64-74` |
| User 路由组 | `prefix = user`，中间件 `user` | `app/Http/Routes/V1/UserRoute.php:23-26` |
| Guest 路由组 | `prefix = guest`，无认证中间件 | `app/Http/Routes/V1/GuestRoute.php:14-16` |
| 认证实现 | `Auth::guard('sanctum')->check()` 失败抛 `ApiException('未登录或登陆已过期', 403)`，故为 **Sanctum Bearer Token** | `app/Http/Middleware/User.php:22-24` |
| 标准信封 | `ApiResponse::success/fail` → `{status, message, data, error}`；HTTP 状态码取 `code` 前 3 位 | `app/Helpers/ApiResponse.php:19-55` |
| success 默认 code/message | `[200001, '操作成功']` | `app/Helpers/ResponseEnum.php:16` |
| fail 默认 code/message | `[200002, '操作失败']` | `app/Helpers/ResponseEnum.php:17` |
| 抛异常的统一出口 | `ApiException` 默认 `code = 400`；`Handler::render` 转成 `fail([code, message])`，即 `{status:'fail', message, data:null, error:null}` | `app/Exceptions/ApiException.php:13-18`、`app/Exceptions/Handler.php:58-69` |
| 未走信封的形式 | 直接 `response([...])` / `response()->json([...])`，常见于 checkout、gift-card/history | 见各接口「例外」字段 |

---

## 1. `app/Http/Controllers/V1/User/PlanController.php`

### 1.1 `fetch`

| 字段 | 值 |
| --- | --- |
| operation | `PlanController@fetch` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/plan/fetch` |
| 认证 | 需要，Bearer（Sanctum），中间件 `user` |
| 请求参数 | `id`：**无任何 validate 规则**（`$request->input('id')` 仅做真值判断），语义上可选；存在则走单条分支，不存在则走列表分支 |
| 响应 | 有 `id` 时 `success(PlanResource::make($plan))`（单对象）；无 `id` 时 `success(PlanResource::collection($plans))`（数组） |
| 未走标准信封的例外 | 无 |
| 失败分支 | `fail([400, __('Subscription plan does not exist')])` — 计划不存在（第 27 行）；`fail([400, __('Subscription plan does not exist')])` — 计划对该用户不可用（第 30 行），两处 message 相同 |
| 源码定位 | `design/Xboard-master/app/Http/Controllers/V1/User/PlanController.php:21-37` |

补充：文件顶部 `use App\Exceptions\ApiException;`（第 5 行）在本类中**未被使用**（源码事实）。
`$plans` 来自 `PlanService::getAvailablePlans()`，过滤条件为 `show = true`、`sell = true`、排序 `sort`，再 `hasCapacity()`（`app/Services/PlanService.php:25-34`）；
单条分支的可用性判断 `isPlanAvailableForUser()`：`user->plan_id === plan->id` 时返回 `plan->renew`，否则要求 `show && sell && hasCapacity()`（`app/Services/PlanService.php:58-67`）。

**PlanResource 展开字段**（`app/Http/Resources/PlanResource.php:20-41`，逐字段）

| 字段 | 来源 / 变换 |
| --- | --- |
| `id` | `resource['id']` |
| `group_id` | `resource['group_id']` |
| `name` | `resource['name']` |
| `tags` | `resource['tags']`（Plan 模型 cast 为 array，`app/Models/Plan.php:100`） |
| `content` | `formatContent()`：把 `{{transfer}}` → `transfer_enable`，`{{speed}}` → `speed_limit` 或「No Limit」，`{{devices}}` → `device_limit` 或「No Limit」，`{{reset_method}}` → 文案（`PlanResource.php:83-121`） |
| `month_price` | `prices['monthly'] * 100`，缺失为 `null` |
| `quarter_price` | `prices['quarterly'] * 100`，缺失为 `null` |
| `half_year_price` | `prices['half_yearly'] * 100`，缺失为 `null` |
| `year_price` | `prices['yearly'] * 100`，缺失为 `null` |
| `two_year_price` | `prices['two_yearly'] * 100`，缺失为 `null` |
| `three_year_price` | `prices['three_yearly'] * 100`，缺失为 `null` |
| `onetime_price` | `prices['onetime'] * 100`，缺失为 `null` |
| `reset_price` | `prices['reset_traffic'] * 100`，缺失为 `null` |
| `capacity_limit` | `null` 原样；`<= 0` 返回 `__('Sold out')` 文案；否则 `(int)` |
| `transfer_enable` | `resource['transfer_enable']` |
| `speed_limit` | `resource['speed_limit']` |
| `device_limit` | `resource['device_limit']` |
| `show` | `(bool)` |
| `sell` | `(bool)` |
| `renew` | `(bool)` |
| `reset_traffic_method` | `resource['reset_traffic_method']` |
| `sort` | `resource['sort']` |
| `created_at` | `resource['created_at']` |
| `updated_at` | `resource['updated_at']` |

（上述周期的旧版键名来自 `Plan::LEGACY_PERIOD_MAPPING`，`app/Models/Plan.php:65-74`。）

---

## 2. `app/Http/Controllers/V1/User/OrderController.php`

### 2.1 `fetch`

| 字段 | 值 |
| --- | --- |
| operation | `OrderController@fetch` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/order/fetch` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `status`：`nullable\|integer\|in:0,1,2,3`，可选；非 null 时才加 where 条件 |
| 响应 | `success(OrderResource::collection($orders))` |
| 未走标准信封的例外 | 无 |
| 失败分支 | 无 `fail()`；参数不合法由 Laravel `ValidationException` 处理（非本控制器信封） |
| 源码定位 | `OrderController.php:23-37`；路由 `UserRoute.php:44` |

查询：`Order::with('plan')->where('user_id', 当前用户)->orderBy('created_at','DESC')`。

### 2.2 `detail`

| 字段 | 值 |
| --- | --- |
| operation | `OrderController@detail` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/order/detail` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `trade_no`：`required\|string`，必填 |
| 响应 | `success(OrderResource::make($order))`；额外注入 `try_out_plan_id`（`(int) admin_setting('try_out_plan_id')`，第 51 行）、`surplus_orders`（`surplus_order_ids` 非空时为 `Order::whereIn('id', ...)->get()` 的模型集合，第 55-57 行） |
| 未走标准信封的例外 | 无 |
| 失败分支 | `fail([400, __('Order does not exist or has been paid')])`（第 49 行）；`fail([400, __('Subscription plan does not exist')])`（第 53 行，`$order->plan` 为空） |
| 源码定位 | `OrderController.php:39-59`；路由 `UserRoute.php:43` |

### 2.3 `save`

| 字段 | 值 |
| --- | --- |
| operation | `OrderController@save` |
| HTTP 方法 | `POST` |
| 完整路径 | `/api/v1/user/order/save` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | 来自 `App\Http\Requests\User\OrderSave`：`plan_id`：`required`，必填（`Requests/User/OrderSave.php:17`）；`period`：`required\|in:month_price,quarter_price,half_year_price,year_price,two_year_price,three_year_price,onetime_price,reset_price`，必填（同文件 18 行）。控制器内又 `validate` 一次：`plan_id`：`required\|exists:App\Models\Plan,id`；`period`：`required\|string`（控制器 63-66 行）。另有未校验参数 `coupon_code`（`$request->input('coupon_code')`，第 84 行，可空）。<br>FormRequest 自定义 message：`Plan ID cannot be empty` / `Plan period cannot be empty` / `Wrong plan period`（`OrderSave.php:22-29`） |
| 响应 | `success($order->trade_no)` — 即 **data 为订单号字符串**，不是对象、不是 Resource |
| 未走标准信封的例外 | 无（走信封，但 data 是标量） |
| 失败分支 | 本方法**没有** `fail()` 调用；全部走 `ApiException` 抛出，由 `Handler::render` 转成 `fail([code, message])`（`code` 默认 400）：`You have an unpaid or pending order, please try again later or cancel it`（第 72 行）；`PlanService::validatePurchase` 抛出 `Subscription plan does not exist` / `This payment period cannot be purchased, please choose another period` / `Current product is sold out` / `This subscription has been sold out, please choose another subscription` / `This subscription cannot be renewed, please change to another subscription` / `This subscription has expired, please change to another subscription` / `Subscription has expired or no active subscription, unable to purchase Data Reset Package`（`PlanService.php:69-93,143-163`）；`OrderService::createFromRequest` 内 `The user does not exist` / `You have an unpaid or pending order...` / `Failed to create order`（`OrderService.php:76,80,109`）；优惠券失败抛 `Coupon failed`（第 94 行，此路径由 `protected applyCoupon` 定义，实际 coupon 处理在 `OrderService` 内，**该 protected 方法在本控制器内未见调用点**） |
| 源码定位 | `OrderController.php:61-88`；路由 `UserRoute.php:40` |

### 2.4 `checkout`

| 字段 | 值 |
| --- | --- |
| operation | `OrderController@checkout` |
| HTTP 方法 | `POST` |
| 完整路径 | `/api/v1/user/order/checkout` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | **无 `validate()`**；实际读取：`trade_no`（无规则，第 120 行）、`method`（支付方式 id，无规则，第 121 行）、`token`（作为 `stripe_token` 传入，第 158 行）。三者均由代码逻辑隐式要求 |
| 响应 | 免费订单：`response(['type' => -1, 'data' => true])`（第 137-140 行）；正常：`response(['type' => $result['type'], 'data' => $result['data']])`（第 160-163 行），`$result` 来自 `PaymentService::pay()` → 支付插件 `pay()` 返回值（`app/Services/PaymentService.php:74-91`）。**`data` 内部结构由具体支付插件决定，未读取** |
| 未走标准信封的例外 | **是**。两条返回路径都直接 `response([...])`，没有 `status/message/error` 字段，HTTP 状态码默认 200 |
| 失败分支 | `fail([400, __('Order does not exist or has been paid')])`（第 127 行）；`fail([400, __('Order amount is invalid')])`（第 130 行）；`fail([400, '支付失败'])`（第 136 行，免费订单 `paid()` 失败）；`fail([400, __('Payment method is not available')])`（第 144 行）；`fail([400, __('Request failed, please try again later')])`（第 153 行，订单保存失败） |
| 源码定位 | `OrderController.php:118-164`；路由 `UserRoute.php:41` |

逻辑补充：查订单条件为 `trade_no` + `user_id` + `status = 0`（第 122-125 行）；`$order->total_amount <= 0` 走免费开通（第 133-141 行）；否则按 `Payment` 的手续费计算 `handling_amount`（整数四舍五入，第 147-150 行）。

### 2.5 `check`

| 字段 | 值 |
| --- | --- |
| operation | `OrderController@check` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/order/check` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `trade_no`：**无 validate**（第 168 行读取），逻辑上必填 |
| 响应 | `success($order->status)` — data 为 **整数状态**（0 待支付 / 1 开通中 / 2 已取消 / 3 已完成 / 4 已折抵，`app/Models/Order.php:56-68`） |
| 未走标准信封的例外 | 无 |
| 失败分支 | `fail([400, __('Order does not exist')])`（第 173 行） |
| 源码定位 | `OrderController.php:166-176`；路由 `UserRoute.php:42` |

### 2.6 `getPaymentMethod`

| 字段 | 值 |
| --- | --- |
| operation | `OrderController@getPaymentMethod` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/order/getPaymentMethod` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | 无（方法签名无参数） |
| 响应 | `success($methods)`，`Payment` 模型集合，**未使用 Resource**；select 白名单字段：`id`、`name`、`payment`、`icon`、`handling_fee_fixed`、`handling_fee_percent`；条件 `enable = 1`，`sort ASC`（第 180-190 行）。`Payment` 模型的 `casts` 存在但未见 `fillable`/`hidden`，因此是否还有额外字段**需以实际 select 为准——已用 select 限定，故仅上述 6 列** |
| 未走标准信封的例外 | 无 |
| 失败分支 | 无 |
| 源码定位 | `OrderController.php:178-193`；路由 `UserRoute.php:45` |

### 2.7 `cancel`

| 字段 | 值 |
| --- | --- |
| operation | `OrderController@cancel` |
| HTTP 方法 | `POST` |
| 完整路径 | `/api/v1/user/order/cancel` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `trade_no`：**无 validate**，仅 `empty()` 判断（第 197 行），逻辑上必填 |
| 响应 | `success(true)` |
| 未走标准信封的例外 | 无 |
| 失败分支 | `fail([422, __('Invalid parameter')])`（第 198 行）；`fail([400, __('Order does not exist')])`（第 204 行）；`fail([400, __('You can only cancel pending orders')])`（第 207 行，`status !== 0`）；`fail([400, __('Cancel failed')])`（第 211 行） |
| 源码定位 | `OrderController.php:195-214`；路由 `UserRoute.php:46` |

### 2.8 非 public 方法（不注册路由，供审计记录）

`protected applyCoupon(Order $order, string $couponCode): void`（第 90-97 行）与 `protected handleUserBalance(Order $order, User $user, UserService $userService): void`（第 99-116 行）。
二者在本控制器内**未见调用点**（`save` 走 `OrderService::createFromRequest`）。`applyCoupon` 失败抛 `ApiException(__('Coupon failed'))`；`handleUserBalance` 失败抛 `ApiException(__('Insufficient balance'))`（未使用 `$order` 参数，源码如此）。

**OrderResource 展开字段**（`app/Http/Resources/OrderResource.php:20-33`）

| 字段 | 来源 / 变换 |
| --- | --- |
| 全量模型属性 | `...parent::toArray($request)`，即 Order 模型可见属性；按 `app/Models/Order.php:9-43` 的属性注解（`infer`：文档块注释，非运行时白名单）包含：`id`、`user_id`、`plan_id`、`payment_id`、`period`、`trade_no`、`total_amount`、`handling_amount`、`balance_amount`、`surplus_credit`、`surplus_amount`、`type`、`status`、`surplus_order_ids`、`coupon_id`、`created_at`、`updated_at`、`commission_status`、`invite_user_id`、`actual_commission_balance`、`commission_rate`、`commission_auto_check`、`commission_balance`、`discount_amount`、`paid_at`、`callback_no` |
| `period` | 被**覆盖**为 `PlanService::getLegacyPeriod((string)$this->period)`，即新周期键转旧键（`app/Services/PlanService.php:137-141`） |
| `plan` | `whenLoaded('plan')` → `PlanResource::make($this->plan)`；`fetch`/`detail` 均 with 了 `plan`，故会出现 |
| `payment` | `whenLoaded('payment')`，结构为 `{id, name, payment, icon}` 或 `null`；仅 `detail` 加载了 `payment`，故 `fetch` 中不出现 |
| `try_out_plan_id` | **仅 detail**：作为动态属性写入，因此会进入 `parent::toArray`（推断；源码 `OrderController.php:51`） |
| `surplus_orders` | **仅 detail 且 `surplus_order_ids` 非空**：`Order` 模型集合，同样是动态属性进入 `parent::toArray`（推断；源码 `OrderController.php:55-57`） |

`created_at` / `updated_at` 由 Order 模型 cast 为 timestamp（`app/Models/Order.php:49-54`）；`surplus_order_ids` cast 为 array，`handling_amount` cast 为 integer。

---

## 3. `app/Http/Controllers/V1/User/CouponController.php`

### 3.1 `check`

| 字段 | 值 |
| --- | --- |
| operation | `CouponController@check` |
| HTTP 方法 | `POST` |
| 完整路径 | `/api/v1/user/coupon/check` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | **无 `validate()`**；实际读取并传入 Service：`code`（仅 `empty()` 判断，必填）、`plan_id`（可空，`setPlanId`）、`period`（可空，`setPeriod`，内部经 `PlanService::getPeriodKey` 转换） |
| 响应 | `success(CouponResource::make($couponService->getCoupon()))` |
| 未走标准信封的例外 | 无 |
| 失败分支 | `fail([422, __('Coupon cannot be empty')])`（第 16 行，`code` 为空）；`CouponService::check()` 抛 `ApiException`（默认 code 400）经 Handler 转 fail：`Invalid coupon`、`This coupon is no longer available`、`This coupon has not yet started`、`This coupon has expired`、`The coupon code cannot be used for this subscription`、`The coupon code cannot be used for this period`、`The coupon can only be used :limit_use_with_user per person`（`app/Services/CouponService.php:90-121`） |
| 源码定位 | `CouponController.php:13-24`；路由 `UserRoute.php:64` |

**CouponResource 展开字段**（`app/Http/Resources/CouponResource.php:24-37`）

| 字段 | 来源 / 变换 |
| --- | --- |
| 全量模型属性 | `...$this->resource->toArray()`；Coupon 表 `v2_coupon`，`$guarded = ['id']`、`$casts`：`limit_plan_ids`/`limit_period` 为 array、`show` 为 boolean、时间戳为 timestamp（`app/Models/Coupon.php:10-19`）。**具体列名在模型层未逐一列出，属未读取**；`limit_period` 有访问器 `getLimitPeriodAttribute`，会把每项经 `PlanService::getPeriodKey` 规范化（`Coupon.php:21-26`）（推断：列集合由数据库迁移决定） |
| `limit_plan_ids` | 为空返回 `null`；否则每项 `(string)` 后的 `values()->all()` 数组 |
| `limit_period` | 为空返回 `null`；否则每项经 `PlanService::convertToLegacyPeriod` 转旧版周期键后的字符串数组 |

---

## 4. `app/Http/Controllers/V1/User/GiftCardController.php`

### 4.1 `check`

| 字段 | 值 |
| --- | --- |
| operation | `GiftCardController@check` |
| HTTP 方法 | `POST` |
| 完整路径 | `/api/v1/user/gift-card/check` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `App\Http\Requests\User\GiftCardCheckRequest` 的 `rules()` **返回空数组**（`GiftCardCheckRequest.php:22-27`），`authorize()` 返回 `true` → **无校验**；控制器实际读取 `code`（第 22 行），必填但无规则 |
| 响应 | `success([...])`，结构：`code_info`、`reward_preview`、`can_redeem`、`reason`（第 35-40 行），**未使用 Resource** |
| 未走标准信封的例外 | 无（但 data 是手工数组，无 Resource） |
| 失败分支 | `fail([400, $e->getMessage()])`（第 44 行，捕获 `ApiException`）；`fail([500, '查询失败，请稍后重试'])`（第 51 行，捕获其他 `\Exception` 并写 `Log::error('礼品卡查询失败')`） |
| 源码定位 | `GiftCardController.php:19-53`；路由 `UserRoute.php:66` |

`code_info` 字段（`app/Services/GiftCardService.php:282-308`）：

| 字段 | 说明 |
| --- | --- |
| `code` | 兑换码原文 |
| `template.name` / `template.description` / `template.type` / `template.type_name` / `template.icon` / `template.background_image` / `template.theme_color` | 模板信息；`type_name` 由 `getTypeNameAttribute` 映射（1 通用礼品卡 / 2 套餐礼品卡 / 3 盲盒礼品卡，`app/Models/GiftCardTemplate.php:34-36,67-82`） |
| `status` / `status_name` / `expires_at` / `usage_count` / `max_usage` | 兑换码状态信息 |
| `plan_info` | **仅当 `template.type === 2`（套餐卡）**，值为 `PlanResource::make($plan)->toArray(request())`，字段同第 1.1 节 PlanResource |

`reward_preview` ＝ `GiftCardTemplate::calculateActualRewards($user)`（`GiftCardService.php:313-320` → `GiftCardTemplate.php:175-218`），返回 **模板 `rewards` JSON 原样（动态键，未固定）**，另可被 `special_config.festival_bonus` 放大数值；可能的键（源码使用处）：`balance`、`transfer_enable`、`device_limit`、`reset_package`、`plan_id`、`plan_validity_days`、`expire_days`、`invite_reward_rate`、`random_rewards`（`GiftCardService.php:167-201,223-247`）。具体键由后台模板配置决定。

`can_redeem`（bool）/ `reason`（string 或 null）来自 `checkUserEligibility()`：`用户信息未提供`、`您不满足此礼品卡的使用条件`、`您已达到此礼品卡的使用限制`、成功时 `null`（`GiftCardService.php:73-97`）。

### 4.2 `redeem`

| 字段 | 值 |
| --- | --- |
| operation | `GiftCardController@redeem` |
| HTTP 方法 | `POST` |
| 完整路径 | `/api/v1/user/gift-card/redeem` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `App\Http\Requests\User\GiftCardRedeemRequest`：`code`：`required\|string\|min:8\|max:32`，必填；`authorize()` 返回 `true`（`GiftCardRedeemRequest.php:14-29`）。自定义 message：`请输入兑换码` / `兑换码长度不能少于8位` / `兑换码长度不能超过32位`（同文件 36-43 行）。另读取 `$request->userAgent()`（第 68 行） |
| 响应 | `success(['message' => '兑换成功！', 'rewards' => ..., 'invite_rewards' => ..., 'template_name' => ...])`（第 77-82 行）；`code`/`ip_address` **未包含在响应中** |
| 未走标准信封的例外 | 无 |
| 失败分支 | `fail([400, $e->getMessage()])`（第 85 行，`ApiException`）；`fail([500, '兑换失败，请稍后重试'])`（第 93 行，其他异常并写 `Log::error`）。可能被捕获的 `ApiException` message：`兑换码不存在`、`该礼品卡类型已停用`、`兑换码不可用：{status_name}`、`您不满足此礼品卡的使用条件`、`您已达到此礼品卡的使用限制`、`未设置使用用户`、`该礼品卡类型不存在`、`用户信息未提供`、`余额发放失败`、`邀请人余额发放失败`、`用户信息更新失败`（`app/Services/GiftCardService.php:25,48,61,65,104,112,117,122,169,233,205`） |
| 源码定位 | `GiftCardController.php:58-95`；路由 `UserRoute.php:67` |

响应子字段：`rewards` ＝ `calculateActualRewards()` 的数组（动态键，见 4.1）；`invite_rewards` ＝ 邀请人奖励或 `null`，可能含 `balance`、`transfer_enable`（`GiftCardService.php:212-250`）；`template_name` 为模板名（`GiftCardService.php:151-156`）。

### 4.3 `history`

| 字段 | 值 |
| --- | --- |
| operation | `GiftCardController@history` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/gift-card/history` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `page`：`integer\|min:1`，可选；`per_page`：`integer\|min:1\|max:100`，可选，默认 15（第 107 行 `$request->input('per_page', 15)`） |
| 响应 | **自定义 JSON**：`{data: [...], pagination: {current_page, last_page, per_page,total}}`（第 129-137 行）。`data` 每项字段：`id`、`code`（前 8 位 + `****`，无 code 时 `''`）、`template_name`、`template_type`、`template_type_name`、`rewards_given`、`invite_rewards`、`multiplier_applied`、`created_at` |
| 未走标准信封的例外 | **是**，`response()->json([...])`，没有 `status/message/error`，也没有 `success()` 信封 |
| 失败分支 | 无 `fail()`；分页参数非法由 Laravel 校验异常处理 |
| 源码定位 | `GiftCardController.php:100-138`；路由 `UserRoute.php:68` |

模型依据：`GiftCardUsage` 表 `v2_gift_card_usage`，casts：`rewards_given` array、`invite_rewards` array、`multiplier_applied` float、`created_at` timestamp（`app/Models/GiftCardUsage.php:28-53`）。

### 4.4 `detail`

| 字段 | 值 |
| --- | --- |
| operation | `GiftCardController@detail` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/gift-card/detail` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | `id`：`required\|integer\|exists:v2_gift_card_usage,id`，必填（第 145-147 行） |
| 响应 | `success([...])`，字段：`id`、`code`（**完整码，未打码**）、`template{name, description, type, type_name, icon, theme_color}`、`rewards_given`、`invite_rewards`、`invite_user{id, email}`（email 打码为前 3 位 + `***@***`；无邀请人时为 `null`）、`user_level_at_use`、`plan_id_at_use`、`multiplier_applied`、`notes`、`created_at`（第 158-181 行）。`ip_address` 被注释掉（第 178 行） |
| 未走标准信封的例外 | 无 |
| 失败分支 | `fail([404, '记录不存在'])`（第 155 行） |
| 源码定位 | `GiftCardController.php:143-182`；路由 `UserRoute.php:69` |

### 4.5 `types`

| 字段 | 值 |
| --- | --- |
| operation | `GiftCardController@types` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/user/gift-card/types` |
| 认证 | 需要，Bearer（Sanctum） |
| 请求参数 | 无（`$request` 未使用） |
| 响应 | `success(['types' => GiftCardTemplate::getTypeMap()])`，即 `{1: '通用礼品卡', 2: '套餐礼品卡', 3: '盲盒礼品卡'}`（`app/Models/GiftCardTemplate.php:67-74`） |
| 未走标准信封的例外 | 无 |
| 失败分支 | 无 |
| 源码定位 | `GiftCardController.php:187-192`；路由 `UserRoute.php:70` |

---

## 5. `app/Http/Controllers/V1/Guest/PlanController.php`

### 5.1 `fetch`

| 字段 | 值 |
| --- | --- |
| operation | `Guest\PlanController@fetch` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/guest/plan/fetch` |
| 认证 | **无**（Guest 路由组无 `user` 中间件，仅 `api` 组） |
| 请求参数 | 无（`$request` 声明但未使用） |
| 响应 | `success(PlanResource::collection($plan))`，字段同第 1.1 节 PlanResource |
| 未走标准信封的例外 | 无 |
| 失败分支 | 无 |
| 源码定位 | `design/Xboard-master/app/Http/Controllers/V1/Guest/PlanController.php:20-24`；路由 `app/Http/Routes/V1/GuestRoute.php:18` |

与 User 版差异（源码事实）：Guest 版**不接受 `id` 参数**，只返回 `getAvailablePlans()` 列表；`use Auth;`（第 9 行）在本类中未被使用。

---

## 6. `app/Http/Controllers/V1/Guest/CommController.php`

### 6.1 `config`

| 字段 | 值 |
| --- | --- |
| operation | `Guest\CommController@config` |
| HTTP 方法 | `GET` |
| 完整路径 | `/api/v1/guest/comm/config` |
| 认证 | **无** |
| 请求参数 | 无（方法无参数） |
| 响应 | `success($data)`，`$data` 逐字段：`tos_url`、`is_email_verify`（0/1）、`is_invite_force`（0/1）、`email_whitelist_suffix`（开启时 `Helper::getEmailSuffix()`，否则整数 `0`）、`is_captcha`（0/1）、`captcha_type`（默认 `recaptcha`）、`recaptcha_site_key`、`recaptcha_v3_site_key`、`recaptcha_v3_score_threshold`（默认 `0.5`）、`turnstile_site_key`、`app_description`、`app_url`、`logo`、`is_recaptcha`（0/1，注释标注「保持向后兼容」）。随后经 `HookManager::filter('guest_comm_config', $data)`，插件可增删字段（第 35 行） |
| 未走标准信封的例外 | 无 |
| 失败分支 | 无 |
| 源码定位 | `design/Xboard-master/app/Http/Controllers/V1/Guest/CommController.php:13-38`；路由 `app/Http/Routes/V1/GuestRoute.php:24` |

---

## 7. 汇总表

| METHOD 路径 | 认证 | 关键请求参数 | data 关键字段 |
| --- | --- | --- | --- |
| GET `/api/v1/user/plan/fetch` | Bearer(Sanctum) | `id`（无校验，可选） | PlanResource[] 或 PlanResource |
| GET `/api/v1/user/order/fetch` | Bearer | `status`(nullable,int,0-3) | OrderResource[] |
| GET `/api/v1/user/order/detail` | Bearer | `trade_no`(required,string) | OrderResource + `try_out_plan_id`、`surplus_orders` |
| POST `/api/v1/user/order/save` | Bearer | `plan_id`(required,exists) `period`(required,in:8周期) `coupon_code`(无校验) | `trade_no` 字符串 |
| POST `/api/v1/user/order/checkout` | Bearer | `trade_no`/`method`/`token`（均无校验） | 无信封：`{type, data}` |
| GET `/api/v1/user/order/check` | Bearer | `trade_no`（无校验） | `status` 整数 |
| GET `/api/v1/user/order/getPaymentMethod` | Bearer | 无 | Payment[]（6 列） |
| POST `/api/v1/user/order/cancel` | Bearer | `trade_no`（无校验，empty 判断） | `true` |
| POST `/api/v1/user/coupon/check` | Bearer | `code`(empty 判断) `plan_id` `period`（后二者无校验） | CouponResource |
| POST `/api/v1/user/gift-card/check` | Bearer | `code`（FormRequest 规则为空） | `code_info`/`reward_preview`/`can_redeem`/`reason` |
| POST `/api/v1/user/gift-card/redeem` | Bearer | `code`(required,string,8-32) | `message`/`rewards`/`invite_rewards`/`template_name` |
| GET `/api/v1/user/gift-card/history` | Bearer | `page` `per_page`(均可选,int) | 无信封：`{data[],pagination{}}` |
| GET `/api/v1/user/gift-card/detail` | Bearer | `id`(required,integer,exists) | 兑换记录详情（`code` 明文） |
| GET `/api/v1/user/gift-card/types` | Bearer | 无 | `types{1,2,3}` |
| GET `/api/v1/guest/plan/fetch` | 无 | 无 | PlanResource[] |
| GET `/api/v1/guest/comm/config` | 无 | 无 | 站点配置 14 字段（可被 Hook 改写） |

## 8. 例外与风险点（源码事实）

1. **未走标准信封的接口共 3 处**：`POST /api/v1/user/order/checkout`（`response(['type','data'])`，`OrderController.php:137-140,160-163`）、`GET /api/v1/user/gift-card/history`（`response()->json(['data','pagination'])`，`GiftCardController.php:129-137`）；另有 checkout 两处分支与正常返回共用该形式。调用方不能依赖 `status/message` 字段。
2. **data 为标量而非对象**：`order/save` 返回 `trade_no` 字符串，`order/check` 返回 `status` 整数，`order/cancel` 返回 `true`。
3. **请求参数缺少校验的位置**（仅做 empty/真值判断或无判断）：`user/plan/fetch` 的 `id`、`order/checkout` 的 `trade_no`+`method`+`token`、`order/check` 与 `order/cancel` 的 `trade_no`、`coupon/check` 的 `code`+`plan_id`+`period`、`gift-card/check` 的 `code`（`GiftCardCheckRequest::rules()` 为空数组）。
4. **响应直接暴露模型/敏感数据**：`order/getPaymentMethod` 直接返回模型集合（无 Resource）；`gift-card/detail` 返回**未打码的完整兑换码**，而 `gift-card/history` 是打码的（前 8 位 + `****`），两处行为不一致。
5. **两处 `Subscription plan does not exist` 语义合并**：`user/plan/fetch` 中「计划不存在」与「计划对该用户不可用」返回相同 code 与 message，客户端无法区分。
6. **同一错误码被复用于不同语义**：`fail([400, ...])` 在 order 系列覆盖订单不存在、金额非法、支付方式不可用、保存失败等多种情况，仅靠 message 区分；HTTP 状态码统一为 400。
7. **认证为 Sanctum**，未登录/过期抛 `ApiException('未登录或登陆已过期', 403)`，因此 403 响应同样走 `{status:'fail', message, data:null, error:null}` 信封（`app/Http/Middleware/User.php:22-24` + `Handler.php:64-68`）。

## 9. 未读取清单

| 项 | 说明 |
| --- | --- |
| `checkout` 中 `data` 的内部结构 | 由具体支付插件 `pay()` 返回，插件实现未读取（`app/Services/PaymentService.php:74-91`） |
| `Coupon` 表完整列集 | 模型未列 `$fillable`/列名，未读取迁移文件（`app/Models/Coupon.php`） |
| `UserService` / `OrderService` 完整分支 | 仅读取本次涉及的 `createFromRequest`、`validatePurchase` 相关片段 |
| `GiftCardCode` 全部字段 | 仅使用 `code`、`status`、`status_name`、`expires_at`、`usage_count`、`max_usage` 等，模型全文未读取 |
