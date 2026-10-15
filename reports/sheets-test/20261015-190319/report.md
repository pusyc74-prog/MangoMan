# Chargesheets test with real free models, 15 October 2026 18:49 UTC

**17 of 17 answers right.**

## 1. Loading

| File | Read |
| --- | --- |
| Product feed (Google Shopping) | 120 rows, 25 s; measures price, sale price; date -; about: (no model description) |
| Sales orders (India) | 230 rows, 25 s; measures Amount; date Order Date; about: (no model description) |
| CRM leads | 150 rows, 25 s; measures Deal Value (INR); date Created; about: (no model description) |
| Ad report (months across) | 12 rows, 25 s; measures Jul Spend, Aug Spend, Sep Spend, Jul Clicks; date -; about: (no model description) |
| Inventory | 60 rows, 25 s; measures Avg Daily Sales, Unit Cost, Qty on Hand, Reorder Level; date Last Restock; about: (no model description) |

Google Sheet link: **works**. Title 'Example Spreadsheet', 30 rows; refresh changed: True 

## 2. Questions

| File | Question | Right | Missing | Unchecked numbers | Steps | Seconds | Answer |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Sales orders (India) | Which channel had the highest net sales in September 2026? | yes |  |  | 1 | 88 | The Website channel had the highest net sales in September 2026, totaling 179,600.00. <br>`SELECT "Channel", ROUND(SUM("Amount"), 2) AS net_sales FROM kesar_sales WHERE strftime('%Y-%m', "Order Date") = '2026-09' GROUP BY "Channel" ORDER BY net_sales DESC LIMIT 50` |
| Sales orders (India) | What were total net sales in August 2026? | yes |  |  | 1 | 82 | The total net sales in August 2026 were ₹552,300.00. <br>`SELECT ROUND(SUM("Amount"), 2) AS total_net_sales FROM kesar_sales WHERE strftime('%Y-%m', "Order Date") = '2026-08'` |
| Sales orders (India) | And in July? | yes |  |  | 1 | 16 | The total net sales in July 2026 were ₹380,800.00. <br>`SELECT round(sum("Amount"),2) AS total_sales FROM kesar_sales WHERE strftime('%Y-%m',"Order Date")='2026-07';` |
| Sales orders (India) | Why did sales drop in September compared to August? | yes |  | 552,300, 340,600, 11,353, 13,808 | 1 | 138 | Sales fell in September because total sales dropped from 552,300 INR in August to 340,600 INR in September. This was driven by fewer orders (30 vs 40) and a lower average spend per order (11,353 INR vs 13,808 INR). All sales channels contributed to the decline, especially Amazon and Website which saw large drops in both order count and revenue. <br>`SELECT strftime('%Y-%m', "Order Date") AS month,
       "Channel",
       SUM("Amount") AS total_sales,
       COUNT(*) AS order_count,
       AVG("Amount") AS avg_amount
FROM kesar_sales
WHERE strftime('%Y-%m', "Order Date") IN ('2026-08','2026-09')
GROUP BY month, "Channel";` |
| Sales orders (India) | How many orders were returns (negative amounts)? | yes |  |  | 1 | 24 | There were 6 orders that were returns. <br>`SELECT COUNT(*) AS return_orders FROM kesar_sales WHERE "Amount" < 0;` |
| Product feed (Google Shopping) | How many products are out of stock? | yes |  |  | 1 | 48 | 24 products are out of stock. <br>`SELECT count(*) FROM pinecrest_feed WHERE "availability" = 'out of stock'` |
| Product feed (Google Shopping) | How many products have no GTIN? | yes |  |  | 0 | 14 | 27 products have no GTIN. <br>`` |
| Product feed (Google Shopping) | What is the average price of the Equipment products? | yes |  |  | 1 | 34 | The average price of Equipment products is $362.62. <br>`SELECT ROUND(AVG("price"),2) AS avg_price FROM pinecrest_feed WHERE "product type" = 'Outdoors > Equipment';` |
| CRM leads | How many leads are in Proposal status? | yes |  |  | 0 | 10 | There are 20 leads in Proposal status. <br>`` |
| CRM leads | Which source brought the most leads? | yes |  |  | 1 | 44 | Referral brought the most leads, with 45 leads. <br>`SELECT "Source", COUNT(*) AS "Lead Count" FROM leads_export GROUP BY "Source" ORDER BY "Lead Count" DESC LIMIT 1;` |
| CRM leads | How many leads have no deal value? | yes |  |  | 0 | 16 | 65 leads have no deal value recorded. <br>`` |
| Ad report (months across) | Which campaign spent the most in total from July to September? | yes |  |  | 1 | 28 | Brand Search spent the most, with a total of 86572 from July to September. <br>`SELECT "Campaign", ROUND("Jul Spend" + "Aug Spend" + "Sep Spend", 2) AS "Total Spend" FROM ads_q3 ORDER BY "Total Spend" DESC LIMIT 1` |
| Ad report (months across) | Which campaigns got zero conversions from July to September? | yes |  |  | 1 | 30 | Display - Remarketing and Video - Awareness had zero conversions in July, August, and September. <br>`SELECT "Campaign" FROM ads_q3 WHERE ("Jul Conv" + "Aug Conv" + "Sep Conv") = 0;` |
| Ad report (months across) | What was the total ad spend in August? | yes |  |  | 1 | 24 | The total ad spend in August was $161,186.00. <br>`SELECT ROUND(SUM("Aug Spend"), 2) AS "Total Aug Spend" FROM ads_q3;` |
| Inventory | How many items have zero quantity on hand? | yes |  |  | 1 | 26 | 2 items have zero quantity on hand. <br>`SELECT COUNT(*) AS zero_qty_items FROM inventory WHERE "Qty on Hand" = 0;` |
| Inventory | Which category has the most items? | yes |  |  | 1 | 52 | Kitchen has the most items, with 23 items in that category. <br>`SELECT "Category", COUNT(*) as item_count FROM inventory GROUP BY "Category" ORDER BY item_count DESC LIMIT 1` |
| Inventory | Which item will run out first at its average daily sales (ignore items with no sales)? | yes |  |  | 1 | 28 | Cotton Jar 57 will run out first, in about 2.89 days at its current average daily sales. <br>`SELECT "Item", ("Qty on Hand" / "Avg Daily Sales") AS days_until FROM inventory WHERE "Avg Daily Sales" > 0 ORDER BY days_until LIMIT 1;` |
